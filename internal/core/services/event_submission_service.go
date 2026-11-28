package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type eventSubmissionService struct {
	repo      ports.EventSubmissionRepository
	access    ports.ViewerAccessRepository
	moderator ports.ContentModerationService
	now       func() time.Time
}

func NewEventSubmissionService(repo ports.EventSubmissionRepository, access ports.ViewerAccessRepository) ports.EventSubmissionService {
	return &eventSubmissionService{repo: repo, access: access, now: time.Now}
}

// UseEventSubmissionModerator revisa los textos públicos del envío (nombre, lugar y lineup). La dirección y el
// contacto no se mandan al moderador.
func UseEventSubmissionModerator(service ports.EventSubmissionService, moderator ports.ContentModerationService) {
	if s, ok := service.(*eventSubmissionService); ok {
		s.moderator = moderator
	}
}

func (s *eventSubmissionService) checkAccess(ctx context.Context, userID string, admin bool) error {
	if s.access == nil {
		return errors.New("viewer access not configured")
	}
	if err := validID(userID); err != nil {
		return err
	}
	denied := domain.ErrKycRequired
	if admin {
		denied = domain.ErrAdminRequired
	}
	v, err := s.access.ViewerAccess(ctx, userID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return denied
	}
	if err != nil {
		return err
	}
	if v.Unavailable || (admin && !v.IsAdmin()) || (!admin && !v.IsKycVerified) {
		return denied
	}
	return nil
}

func (s *eventSubmissionService) Create(ctx context.Context, userID string, input domain.EventSubmissionInput) (domain.EventSubmission, error) {
	if err := s.checkAccess(ctx, userID, false); err != nil {
		return domain.EventSubmission{}, err
	}
	normalized, err := domain.NormalizeSubmission(input, userID, s.now())
	if err != nil {
		return domain.EventSubmission{}, err
	}
	if s.moderator != nil {
		text := strings.Join([]string{normalized.Name, normalized.VenueName, deref(normalized.Lineup)}, "\n")
		if err := s.moderator.ValidateContent(ctx, text); err != nil {
			return domain.EventSubmission{}, err
		}
	}
	return s.repo.Create(ctx, userID, normalized)
}

func (s *eventSubmissionService) ListMine(ctx context.Context, userID string) ([]domain.EventSubmission, error) {
	if err := validID(userID); err != nil {
		return nil, err
	}
	return s.repo.ListMine(ctx, userID)
}

func (s *eventSubmissionService) AdminList(ctx context.Context, adminID, status string, limit, offset int) ([]domain.AdminEventSubmission, error) {
	if err := s.checkAccess(ctx, adminID, true); err != nil {
		return nil, err
	}
	switch status {
	case "":
		status = domain.SubmissionPending
	case domain.SubmissionPending, domain.SubmissionPublished, domain.SubmissionRejected:
	default:
		return nil, &domain.ValidationError{Message: "status must be pending, published or rejected"}
	}
	return s.repo.AdminList(ctx, status, clamp(limit, 50, 100), max(offset, 0))
}

func (s *eventSubmissionService) Publish(ctx context.Context, adminID, submissionID string) (string, error) {
	if err := validID(submissionID); err != nil {
		return "", err
	}
	if err := s.checkAccess(ctx, adminID, true); err != nil {
		return "", err
	}
	return s.repo.Publish(ctx, submissionID, adminID)
}

func (s *eventSubmissionService) Reject(ctx context.Context, adminID, submissionID, reason string) error {
	if err := validID(submissionID); err != nil {
		return err
	}
	reason, err := domain.NormalizeRejectReason(reason)
	if err != nil {
		return err
	}
	if err := s.checkAccess(ctx, adminID, true); err != nil {
		return err
	}
	return s.repo.Reject(ctx, submissionID, adminID, reason)
}

// Similar: con menos de 3 letras no busca (devuelve []).
func (s *eventSubmissionService) Similar(ctx context.Context, name string, date *time.Time) ([]domain.Event, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 {
		return []domain.Event{}, nil
	}
	if len([]rune(name)) > domain.SubmissionNameMax {
		name = string([]rune(name)[:domain.SubmissionNameMax])
	}
	return s.repo.Similar(ctx, name, date, domain.SimilarEventsMax)
}
