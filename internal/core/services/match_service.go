package services

import (
	"context"
	"root-backend-service/internal/core/domain"
	"time"

	"github.com/google/uuid"
)

type MatchRepository interface {
	CreateSwipe(ctx context.Context, swipe *domain.EventSwipe) error
	GetPendingSwipes(ctx context.Context, eventID string) ([]*domain.EventSwipe, error)
	UpdateSwipeHasCrew(ctx context.Context, swipeID string, hasCrew bool) error
	CreateSquad(ctx context.Context, squad *domain.Squad) error
	AddSquadMember(ctx context.Context, member *domain.SquadMember) error
}

type MatchService struct {
	repo MatchRepository
}

func NewMatchService(repo MatchRepository) *MatchService {
	return &MatchService{repo: repo}
}

func (s *MatchService) ProcessSwipe(ctx context.Context, swipe *domain.EventSwipe) (map[string]interface{}, error) {
	swipe.ID = uuid.New().String()
	swipe.CreatedAt = time.Now()
	swipe.HasCrew = false

	if err := s.repo.CreateSwipe(ctx, swipe); err != nil {
		return nil, err
	}

	// Basic matching logic: check other pending swipes
	swipes, err := s.repo.GetPendingSwipes(ctx, swipe.EventID)
	if err != nil {
		return nil, err
	}

	var bestMatch *domain.EventSwipe
	for _, candidate := range swipes {
		if candidate.UserID != swipe.UserID && !candidate.HasCrew {
			// Fake logic: first one found becomes match
			bestMatch = candidate
			break
		}
	}

	if bestMatch != nil {
		// Create new squad
		squadID := uuid.New().String()
		squad := &domain.Squad{
			ID:         squadID,
			EventID:    swipe.EventID,
			Name:       "New Crew",
			Status:     "forming",
			ChatRoomID: uuid.New().String(),
			CreatedAt:  time.Now(),
			ExpiresAt:  time.Now().Add(24 * time.Hour),
		}
		_ = s.repo.CreateSquad(ctx, squad)

		_ = s.repo.AddSquadMember(ctx, &domain.SquadMember{SquadID: squadID, UserID: swipe.UserID, Role: "member", JoinedAt: time.Now()})
		_ = s.repo.AddSquadMember(ctx, &domain.SquadMember{SquadID: squadID, UserID: bestMatch.UserID, Role: "member", JoinedAt: time.Now()})

		_ = s.repo.UpdateSwipeHasCrew(ctx, swipe.ID, true)
		_ = s.repo.UpdateSwipeHasCrew(ctx, bestMatch.ID, true)

		return map[string]interface{}{
			"status": "matched",
			"crew": map[string]interface{}{
				"id":           squad.ID,
				"name":         squad.Name,
				"membersCount": 2,
				"matchScore":   100,
			},
		}, nil
	}

	return map[string]interface{}{
		"status":  "queued",
		"message": "Swipe registrado. Buscando personas compatibles...",
	}, nil
}
