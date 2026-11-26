package services

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type tempCrewService struct {
	repo      ports.TempCrewRepository
	moderator ports.ContentModerationService
}

// UseTempCrewModerator revisa el nombre, el plan y los mensajes de solicitud antes de guardarlos.
func UseTempCrewModerator(service ports.TempCrewService, moderator ports.ContentModerationService) {
	if s, ok := service.(*tempCrewService); ok {
		s.moderator = moderator
	}
}

func (s *tempCrewService) moderate(ctx context.Context, text string) error {
	if s.moderator == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	return s.moderator.ValidateContent(ctx, text)
}

func NewTempCrewService(repo ports.TempCrewRepository) ports.TempCrewService {
	return &tempCrewService{repo: repo}
}

func validID(values ...string) error {
	for _, v := range values {
		if _, err := uuid.Parse(v); err != nil {
			return domain.ErrInvalidID
		}
	}
	return nil
}

func clamp(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func (s *tempCrewService) ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.TempCrewListPage, error) {
	if err := validID(eventID); err != nil {
		return domain.TempCrewListPage{}, err
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListForEvent(ctx, eventID, viewerID, clamp(limit, 20, 50), offset)
}

// eventSummaryPreview es la cantidad de crews que el detalle del evento muestra de entrada.
const eventSummaryPreview = 3

func (s *tempCrewService) EventSummary(ctx context.Context, eventID, viewerID string) (*domain.TempCrewsEventSummary, error) {
	if err := validID(eventID); err != nil {
		return nil, err
	}
	return s.repo.EventSummary(ctx, eventID, viewerID, eventSummaryPreview)
}

func (s *tempCrewService) Create(ctx context.Context, hostID, eventID string, input domain.CreateTempCrewInput) (*domain.TempCrewSummary, error) {
	if err := validID(eventID); err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Plan = strings.TrimSpace(input.Plan)
	switch {
	case input.Name == "" || utf8.RuneCountInString(input.Name) > domain.TempCrewNameMax:
		return nil, &domain.ValidationError{Message: "name must have between 1 and 40 characters"}
	case input.Plan == "" || utf8.RuneCountInString(input.Plan) > domain.TempCrewPlanMax:
		return nil, &domain.ValidationError{Message: "plan must have between 1 and 280 characters"}
	case input.Capacity < domain.TempCrewMinCapacity || input.Capacity > domain.TempCrewMaxCapacity:
		return nil, &domain.ValidationError{Message: "capacity must be between 2 and 10"}
	}
	if err := s.moderate(ctx, input.Name+"\n"+input.Plan); err != nil {
		return nil, err
	}
	crewID, err := s.repo.Create(ctx, hostID, eventID, input)
	if err != nil {
		return nil, err
	}
	return s.repo.Summary(ctx, crewID, hostID)
}

func (s *tempCrewService) Detail(ctx context.Context, crewID, viewerID string) (*domain.TempCrewDetail, error) {
	if err := validID(crewID); err != nil {
		return nil, err
	}
	return s.repo.Detail(ctx, crewID, viewerID)
}

func (s *tempCrewService) RequestJoin(ctx context.Context, crewID, userID, message string) (domain.TempCrewJoinRequest, bool, error) {
	if err := validID(crewID); err != nil {
		return domain.TempCrewJoinRequest{}, false, err
	}
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > domain.TempCrewMessageMax {
		return domain.TempCrewJoinRequest{}, false, &domain.ValidationError{Message: "message must have at most 280 characters"}
	}
	if err := s.moderate(ctx, message); err != nil {
		return domain.TempCrewJoinRequest{}, false, err
	}
	var msg *string
	if message != "" {
		msg = &message
	}
	return s.repo.RequestJoin(ctx, crewID, userID, msg)
}

func (s *tempCrewService) CancelMyRequest(ctx context.Context, crewID, userID string) error {
	if err := validID(crewID); err != nil {
		return err
	}
	return s.repo.CancelMyRequest(ctx, crewID, userID)
}

func (s *tempCrewService) ListRequests(ctx context.Context, crewID, hostID, status string, limit, offset int) ([]domain.TempCrewJoinRequest, error) {
	if err := validID(crewID); err != nil {
		return nil, err
	}
	switch status {
	case "":
		status = domain.JoinRequestPending
	case domain.JoinRequestPending, domain.JoinRequestRejected:
	default:
		return nil, &domain.ValidationError{Message: "status must be pending or rejected"}
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListRequests(ctx, crewID, hostID, status, clamp(limit, 50, 100), offset)
}

func (s *tempCrewService) Decide(ctx context.Context, crewID, requestID, hostID string, accept bool) (domain.TempCrewJoinRequest, error) {
	if err := validID(crewID, requestID); err != nil {
		return domain.TempCrewJoinRequest{}, err
	}
	return s.repo.Decide(ctx, crewID, requestID, hostID, accept)
}

func (s *tempCrewService) SetAdmitting(ctx context.Context, crewID, hostID string, admitting bool) (bool, error) {
	if err := validID(crewID); err != nil {
		return false, err
	}
	return s.repo.SetAdmitting(ctx, crewID, hostID, admitting)
}

func (s *tempCrewService) ListMine(ctx context.Context, userID string) ([]domain.TempCrewSummary, error) {
	return s.repo.ListMine(ctx, userID)
}

func (s *tempCrewService) Discover(ctx context.Context, userID string, limit int) ([]domain.TempCrewSummary, error) {
	return s.repo.Discover(ctx, userID, clamp(limit, 20, 50))
}

func (s *tempCrewService) CloseEnded(ctx context.Context) (int64, error) {
	return s.repo.CloseEnded(ctx)
}
