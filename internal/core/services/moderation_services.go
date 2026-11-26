package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// ---------- Baja de cuenta ----------

// AccountService da de baja la cuenta al instante y purga sus datos cuando vence la retención.
type AccountService struct {
	repo ports.AccountRepository
	// beforeDelete corre antes de la baja (revocar Sign in with Apple). Si falla, se loguea y la baja sigue igual.
	beforeDelete func(ctx context.Context, userID string) error
	// invalidateSession saca la sesión del caché para que el JWT deje de valer enseguida.
	invalidateSession func(userID string)
	files             ports.ObjectDeleter
}

func NewAccountService(repo ports.AccountRepository, invalidateSession func(userID string), files ports.ObjectDeleter) *AccountService {
	return &AccountService{repo: repo, invalidateSession: invalidateSession, files: files}
}

// BeforeDelete registra un paso que corre antes de dar de baja la cuenta (por ejemplo, revocar Sign in with Apple).
func (s *AccountService) BeforeDelete(step func(ctx context.Context, userID string) error) {
	s.beforeDelete = step
}

func (s *AccountService) DeleteAccount(ctx context.Context, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return domain.ErrUserNotFound
	}
	if s.beforeDelete != nil {
		if err := s.beforeDelete(ctx, userID); err != nil {
			log.Printf("account %s: before delete: %v", userID, err)
		}
	}
	if err := s.repo.DeleteAccount(ctx, userID, domain.AccountDeletionRetention); err != nil {
		return err
	}
	if s.invalidateSession != nil {
		s.invalidateSession(userID)
	}
	return nil
}

// PurgeDue borra los archivos y los datos de las cuentas cuya retención venció. Si un archivo no se puede borrar,
// esa cuenta queda pendiente para la próxima corrida. Devuelve cuántas cuentas purgó.
func (s *AccountService) PurgeDue(ctx context.Context) (int, error) {
	purges, err := s.repo.DuePurges(ctx, 50)
	if err != nil {
		return 0, err
	}
	var failures []error
	done := 0
	for _, purge := range purges {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		if err := s.deleteFiles(ctx, purge.ObjectKeys); err != nil {
			failures = append(failures, fmt.Errorf("account %s: %w", purge.UserID, err))
			continue
		}
		if err := s.repo.CompletePurge(ctx, purge.UserID); err != nil {
			failures = append(failures, err)
			continue
		}
		done++
	}
	return done, errors.Join(failures...)
}

func (s *AccountService) deleteFiles(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if s.files == nil {
		return errors.New("file storage is not configured")
	}
	for _, key := range keys {
		if err := s.files.DeleteObject(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// ---------- Bloqueos ----------

// BlockService bloquea y desbloquea por username.
type BlockService struct {
	blocks ports.BlockRepository
	users  ports.UserRepository
}

func NewBlockService(blocks ports.BlockRepository, users ports.UserRepository) *BlockService {
	return &BlockService{blocks: blocks, users: users}
}

func (s *BlockService) target(ctx context.Context, blockerID, username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return "", domain.ErrUserNotFound
	}
	user, err := s.users.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if user.ID == blockerID {
		return "", domain.ErrCannotBlockSelf
	}
	return user.ID, nil
}

func (s *BlockService) Block(ctx context.Context, blockerID, targetUsername string) error {
	targetID, err := s.target(ctx, blockerID, targetUsername)
	if err != nil {
		return err
	}
	return s.blocks.Block(ctx, blockerID, targetID)
}

// Unblock es idempotente: desbloquear a alguien que no estaba bloqueado no es un error.
func (s *BlockService) Unblock(ctx context.Context, blockerID, targetUsername string) error {
	targetID, err := s.target(ctx, blockerID, targetUsername)
	if err != nil {
		return err
	}
	_, err = s.blocks.Unblock(ctx, blockerID, targetID)
	return err
}

func (s *BlockService) List(ctx context.Context, blockerID string) ([]domain.BlockedUser, error) {
	return s.blocks.ListBlocked(ctx, blockerID)
}

// ---------- Denuncias ----------

// ReportService valida y guarda denuncias; ProcessNotifications avisa al soporte de las nuevas.
type ReportService struct {
	repo     ports.ReportRepository
	notifier ports.ReportNotifier
}

func NewReportService(repo ports.ReportRepository, notifier ports.ReportNotifier) *ReportService {
	return &ReportService{repo: repo, notifier: notifier}
}

func (s *ReportService) Create(ctx context.Context, reporterID string, input domain.ReportInput) (string, bool, error) {
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.Details = strings.TrimSpace(input.Details)
	if reporterID == "" || !input.TargetType.Valid() {
		return "", false, domain.ErrInvalidReport
	}
	if _, err := uuid.Parse(input.TargetID); err != nil {
		return "", false, domain.ErrInvalidReport
	}
	switch input.Reason {
	case domain.ReportReasonSpam, domain.ReportReasonAbuse, domain.ReportReasonOther:
	default:
		return "", false, domain.ErrInvalidReport
	}
	if len([]rune(input.Details)) > domain.MaxReportDetails {
		return "", false, domain.ErrInvalidReport
	}
	return s.repo.CreateReport(ctx, reporterID, input)
}

// ProcessNotifications manda por email las denuncias que todavía no se avisaron. Si el envío falla, quedan
// pendientes para el próximo intento. Sin notificador configurado no hace nada.
func (s *ReportService) ProcessNotifications(ctx context.Context) error {
	if s.notifier == nil {
		return nil
	}
	reports, err := s.repo.PendingNotifications(ctx, 20)
	if err != nil {
		return err
	}
	var failures []error
	for _, report := range reports {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.notifier.SendReportNotification(ctx, report); err != nil {
			failures = append(failures, fmt.Errorf("report %s: %w", report.ID, err))
			continue
		}
		if err := s.repo.MarkNotified(ctx, report.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
