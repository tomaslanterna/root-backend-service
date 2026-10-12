package services

import (
	"context"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type BadgeService struct{ repo ports.BadgeRepository }

func NewBadgeService(repo ports.BadgeRepository) *BadgeService { return &BadgeService{repo: repo} }
func (s *BadgeService) List(ctx context.Context, username, viewer string, limit, offset int) ([]domain.EventBadge, int, error) {
	return s.repo.List(ctx, username, viewer, limit, offset)
}
func (s *BadgeService) Get(ctx context.Context, userID, eventID string) (*domain.EventBadge, error) {
	return s.repo.Get(ctx, userID, eventID)
}
