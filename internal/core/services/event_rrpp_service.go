package services

import (
	"context"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type eventRRPPService struct{ repo ports.EventRRPPRepository }

func NewEventRRPPService(repo ports.EventRRPPRepository) ports.EventRRPPService {
	return &eventRRPPService{repo: repo}
}

func (s *eventRRPPService) ListForEvent(ctx context.Context, eventID string) ([]domain.EventRRPP, error) {
	if err := validID(eventID); err != nil {
		return nil, err
	}
	rrpps, err := s.repo.ListForEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if rrpps == nil {
		rrpps = []domain.EventRRPP{}
	}
	return rrpps, nil
}

func (s *eventRRPPService) Assign(ctx context.Context, eventID, userID string, position int) error {
	if err := validID(eventID, userID); err != nil {
		return err
	}
	if position < 0 || position > domain.EventRRPPMaxPosition {
		return domain.ErrInvalidRRPPPosition
	}
	return s.repo.Assign(ctx, eventID, userID, position)
}

func (s *eventRRPPService) Remove(ctx context.Context, eventID, userID string) (bool, error) {
	if err := validID(eventID, userID); err != nil {
		return false, err
	}
	return s.repo.Remove(ctx, eventID, userID)
}
