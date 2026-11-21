package services

import (
	"context"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"
)

type MatchRepository interface {
	ProcessSwipe(ctx context.Context, swipe *domain.EventSwipe) (*domain.MatcherSquad, error)
	GetUserSquads(ctx context.Context, userID string) ([]domain.MatcherSquad, error)
	EnsureSquadChat(ctx context.Context, squadID, userID string) (string, error)
}

type MatchService struct {
	repo          MatchRepository
	notifications ports.NotificationPublisher
}

func NewMatchService(repo MatchRepository, publishers ...ports.NotificationPublisher) *MatchService {
	service := &MatchService{repo: repo}
	if len(publishers) > 0 {
		service.notifications = publishers[0]
	}
	return service
}

func (s *MatchService) ProcessSwipe(ctx context.Context, swipe *domain.EventSwipe) (map[string]interface{}, error) {
	if _, err := uuid.Parse(swipe.EventID); err != nil {
		return nil, domain.ErrInvalidSwipe
	}
	if _, err := uuid.Parse(swipe.UserID); err != nil {
		return nil, domain.ErrInvalidSwipe
	}
	if swipe.Direction != "like" && swipe.Direction != "superlike" && swipe.Direction != "pass" {
		return nil, domain.ErrInvalidSwipe
	}
	swipe.ID = uuid.NewString()
	swipe.CreatedAt = time.Now().UTC()
	swipe.HasCrew = false
	crew, err := s.repo.ProcessSwipe(ctx, swipe)
	if err != nil {
		return nil, err
	}
	if crew != nil {
		if s.notifications != nil {
			for _, member := range crew.Members {
				_ = s.notifications.Publish(ctx, domain.NotificationInput{UserID: member.UserID, Type: "crew.match", Category: domain.NotificationCrews,
					Title: "¡Se armó tu crew!", Body: "Ya tenés grupo para " + crew.EventTitle + ".", Data: map[string]string{"squad_id": crew.ID}, DedupeKey: "crew.match:" + crew.ID + ":" + member.UserID})
			}
		}
		return map[string]interface{}{"status": "matched", "crew": crew}, nil
	}
	return map[string]interface{}{"status": "queued", "message": "Swipe registrado. Buscando personas compatibles..."}, nil
}
func (s *MatchService) GetUserSquads(ctx context.Context, userID string) ([]domain.MatcherSquad, error) {
	return s.repo.GetUserSquads(ctx, userID)
}
func (s *MatchService) EnsureSquadChat(ctx context.Context, squadID, userID string) (string, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return "", domain.ErrInvalidSwipe
	}
	return s.repo.EnsureSquadChat(ctx, squadID, userID)
}
