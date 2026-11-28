package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type afterService struct {
	repo      ports.AfterRepository
	access    ports.ViewerAccessRepository
	moderator ports.ContentModerationService
	now       func() time.Time
}

func NewAfterService(repo ports.AfterRepository, access ports.ViewerAccessRepository) ports.AfterService {
	return &afterService{repo: repo, access: access, now: time.Now}
}

// UseAfterModerator revisa los textos públicos del after (nombre, zona, reglas, lineup y aviso) antes de guardarlos.
// La dirección nunca se manda al moderador.
func UseAfterModerator(service ports.AfterService, moderator ports.ContentModerationService) {
	if s, ok := service.(*afterService); ok {
		s.moderator = moderator
	}
}

func (s *afterService) moderate(ctx context.Context, texts ...string) error {
	if s.moderator == nil {
		return nil
	}
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return s.moderator.ValidateContent(ctx, strings.Join(parts, "\n"))
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sameString(a, b *string) bool { return deref(a) == deref(b) }

// viewer lee el acceso de quien consulta. Una cuenta inexistente, eliminada o suspendida no pasa.
func (s *afterService) viewer(ctx context.Context, userID string) (domain.ViewerAccess, error) {
	if s.access == nil {
		return domain.ViewerAccess{}, errors.New("viewer access not configured")
	}
	if err := validID(userID); err != nil {
		return domain.ViewerAccess{}, err
	}
	v, err := s.access.ViewerAccess(ctx, userID)
	if err != nil {
		return v, fmt.Errorf("reading viewer access: %w", err)
	}
	return v, nil
}

func (s *afterService) requireKyc(ctx context.Context, userID string) error {
	v, err := s.viewer(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.ErrKycRequired
		}
		return err
	}
	if v.Unavailable || !v.IsKycVerified {
		return domain.ErrKycRequired
	}
	return nil
}

func (s *afterService) requireAdult(ctx context.Context, userID string) error {
	v, err := s.viewer(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.ErrUnderage
		}
		return err
	}
	if v.Unavailable || !domain.IsAdult(v.Dob, s.now()) {
		return domain.ErrUnderage
	}
	return nil
}

func (s *afterService) requireAdmin(ctx context.Context, userID string) error {
	v, err := s.viewer(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.ErrAdminRequired
		}
		return err
	}
	if v.Unavailable || !v.IsAdmin() {
		return domain.ErrAdminRequired
	}
	return nil
}

func (s *afterService) ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.AfterPage, error) {
	if err := validID(eventID); err != nil {
		return domain.AfterPage{}, err
	}
	return s.repo.ListForEvent(ctx, eventID, viewerID, clamp(limit, 20, 50), max(offset, 0))
}

func (s *afterService) List(ctx context.Context, filter domain.AfterFilter, viewerID string) (domain.AfterPage, error) {
	now := s.now()
	if filter.From.IsZero() {
		filter.From = now
	}
	if filter.Until != nil && filter.Until.Before(filter.From) {
		return domain.AfterPage{}, &domain.ValidationError{Message: "until must be after from"}
	}
	filter.Zone = strings.TrimSpace(filter.Zone)
	if len([]rune(filter.Zone)) > domain.AfterZoneMax {
		return domain.AfterPage{}, &domain.ValidationError{Message: "zone must have at most 60 characters"}
	}
	filter.Limit, filter.Offset = clamp(filter.Limit, 20, 50), max(filter.Offset, 0)
	return s.repo.List(ctx, filter, viewerID)
}

// Detail: hace falta sesión y ser mayor de edad (el host ve siempre su after). La privacidad se aplica acá.
func (s *afterService) Detail(ctx context.Context, afterID, viewerID string) (*domain.AfterDetail, error) {
	if err := validID(afterID, viewerID); err != nil {
		return nil, err
	}
	row, err := s.repo.Detail(ctx, afterID, viewerID)
	if err != nil {
		return nil, err
	}
	isHost := row.Detail.Host.UserID == viewerID
	if !isHost {
		if err := s.requireAdult(ctx, viewerID); err != nil {
			return nil, err
		}
		// Archivado u oculto: solo lo ve el host.
		if row.StoredStatus != domain.AfterStatusActive || row.Archived {
			return nil, domain.ErrAfterNotFound
		}
	}
	if row.StoredStatus == domain.AfterStatusRemoved {
		return nil, domain.ErrAfterNotFound
	}
	detail := row.ForViewer()
	return &detail, nil
}

func (s *afterService) eventWindow(ctx context.Context, eventID *string) (*domain.AfterEventWindow, error) {
	if eventID == nil {
		return nil, nil
	}
	if err := validID(*eventID); err != nil {
		return nil, domain.ErrInvalidEvent
	}
	window, err := s.repo.EventWindow(ctx, *eventID)
	if errors.Is(err, domain.ErrEventNotFound) {
		return nil, domain.ErrInvalidEvent
	}
	if err != nil {
		return nil, err
	}
	return &window, nil
}

func (s *afterService) Create(ctx context.Context, hostID string, fields domain.AfterFields) (*domain.AfterDetail, error) {
	if err := s.requireKyc(ctx, hostID); err != nil {
		return nil, err
	}
	now := s.now()
	normalized := fields
	normalized.EventID = trimOptionalID(fields.EventID)
	event, err := s.eventWindow(ctx, normalized.EventID)
	if err != nil {
		return nil, err
	}
	normalized, err = domain.NormalizeAfter(normalized, domain.AfterValidation{
		Now: now, HostID: hostID, CheckStart: true, Event: event, CheckEventTo: true,
	})
	if err != nil {
		return nil, err
	}
	if err := s.moderate(ctx, normalized.Name, normalized.Zone, deref(normalized.Rules), deref(normalized.Lineup)); err != nil {
		return nil, err
	}
	afterID, err := s.repo.Create(ctx, hostID, normalized, now)
	if err != nil {
		return nil, err
	}
	return s.hostDetail(ctx, afterID, hostID)
}

func (s *afterService) hostDetail(ctx context.Context, afterID, hostID string) (*domain.AfterDetail, error) {
	row, err := s.repo.Detail(ctx, afterID, hostID)
	if err != nil {
		return nil, err
	}
	detail := row.ForViewer()
	return &detail, nil
}

func trimOptionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (s *afterService) Update(ctx context.Context, afterID, hostID string, patch domain.AfterPatch) (*domain.AfterDetail, error) {
	if err := validID(afterID); err != nil {
		return nil, err
	}
	if err := s.requireKyc(ctx, hostID); err != nil {
		return nil, err
	}
	now := s.now()
	current, err := s.repo.Current(ctx, afterID, now)
	if err != nil {
		return nil, err
	}
	if current.HostID != hostID {
		return nil, domain.ErrNotAfterHost
	}
	if current.Archived {
		return nil, domain.ErrAfterEnded
	}
	if patch.EventID.Set {
		patch.EventID.Value = trimOptionalID(patch.EventID.Value)
	}
	next := patch.Apply(current.Fields)
	startChanged := !next.StartsAt.Equal(current.Fields.StartsAt)
	eventChanged := !sameString(next.EventID, current.Fields.EventID)
	var event *domain.AfterEventWindow
	if eventChanged || startChanged {
		if event, err = s.eventWindow(ctx, next.EventID); err != nil {
			return nil, err
		}
	}
	next, err = domain.NormalizeAfter(next, domain.AfterValidation{
		Now: now, HostID: hostID, CheckStart: startChanged, Event: event, CheckEventTo: eventChanged || startChanged,
	})
	if err != nil {
		return nil, err
	}
	notice := domain.NoticeChange{}
	if patch.Notice.Set {
		value, err := domain.NormalizeNotice(patch.Notice.Value)
		if err != nil {
			return nil, err
		}
		if !sameString(value, current.Notice) {
			notice = domain.NoticeChange{Changed: true, Value: value}
		}
	}
	// Solo se modera lo que cambió.
	var changed []string
	if next.Name != current.Fields.Name {
		changed = append(changed, next.Name)
	}
	if next.Zone != current.Fields.Zone {
		changed = append(changed, next.Zone)
	}
	if !sameString(next.Rules, current.Fields.Rules) {
		changed = append(changed, deref(next.Rules))
	}
	if !sameString(next.Lineup, current.Fields.Lineup) {
		changed = append(changed, deref(next.Lineup))
	}
	if notice.Changed {
		changed = append(changed, deref(notice.Value))
	}
	if err := s.moderate(ctx, changed...); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, afterID, hostID, next, notice); err != nil {
		return nil, err
	}
	return s.hostDetail(ctx, afterID, hostID)
}

func (s *afterService) Remove(ctx context.Context, afterID, hostID string) error {
	if err := validID(afterID); err != nil {
		return err
	}
	return s.repo.Remove(ctx, afterID, hostID)
}

func (s *afterService) Join(ctx context.Context, afterID, userID, message string) (*domain.AfterDetail, error) {
	if err := validID(afterID); err != nil {
		return nil, err
	}
	if err := s.requireAdult(ctx, userID); err != nil {
		return nil, err
	}
	message = strings.TrimSpace(message)
	if len([]rune(message)) > domain.AfterMessageMax {
		return nil, &domain.ValidationError{Message: "message must have at most 280 characters"}
	}
	if err := s.moderate(ctx, message); err != nil {
		return nil, err
	}
	var msg *string
	if message != "" {
		msg = &message
	}
	if _, err := s.repo.Join(ctx, afterID, userID, msg); err != nil {
		return nil, err
	}
	return s.Detail(ctx, afterID, userID)
}

func (s *afterService) Leave(ctx context.Context, afterID, userID string) error {
	if err := validID(afterID); err != nil {
		return err
	}
	return s.repo.Leave(ctx, afterID, userID)
}

func (s *afterService) ListRequests(ctx context.Context, afterID, hostID, status string) ([]domain.AfterRequest, error) {
	if err := validID(afterID); err != nil {
		return nil, err
	}
	switch status {
	case "":
		status = domain.AttendeeRequested
	case domain.AttendeeRequested, domain.AttendeeAccepted, domain.AttendeeRejected, domain.AttendeeGoing:
	default:
		return nil, &domain.ValidationError{Message: "status must be requested, accepted, rejected or going"}
	}
	return s.repo.ListRequests(ctx, afterID, hostID, status)
}

func (s *afterService) Decide(ctx context.Context, afterID, hostID, userID string, accept bool) error {
	if err := validID(afterID, userID); err != nil {
		return err
	}
	return s.repo.Decide(ctx, afterID, hostID, userID, accept)
}

func (s *afterService) Report(ctx context.Context, afterID, userID, reason string) error {
	if err := validID(afterID); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > domain.AfterReasonMax {
		return &domain.ValidationError{Message: "reason must have between 1 and 280 characters"}
	}
	return s.repo.Report(ctx, afterID, userID, reason)
}

func (s *afterService) AdminList(ctx context.Context, adminID, status string, limit, offset int) ([]domain.AdminAfter, error) {
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	switch status {
	case "":
		status = domain.AfterStatusHidden
	case domain.AfterStatusHidden, domain.AfterStatusActive, domain.AfterStatusRemoved:
	default:
		return nil, &domain.ValidationError{Message: "status must be hidden, active or removed"}
	}
	return s.repo.AdminList(ctx, status, clamp(limit, 50, 100), max(offset, 0))
}

func (s *afterService) AdminSetStatus(ctx context.Context, adminID, afterID, status string) error {
	if err := validID(afterID); err != nil {
		return err
	}
	if status != domain.AfterStatusActive && status != domain.AfterStatusRemoved {
		return &domain.ValidationError{Message: "invalid status"}
	}
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	return s.repo.AdminSetStatus(ctx, afterID, adminID, status)
}
