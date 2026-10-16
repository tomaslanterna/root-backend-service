package services

import (
	"context"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type ReviewReminderService struct {
	repo ports.ReviewReminderRepository
}

func NewReviewReminderService(repo ports.ReviewReminderRepository) *ReviewReminderService {
	return &ReviewReminderService{repo: repo}
}
func (s *ReviewReminderService) Checkout(ctx context.Context, user, event string) error {
	if _, err := uuid.Parse(event); err != nil {
		return domain.ErrInvalidSurvey
	}
	return s.repo.Checkout(ctx, user, event)
}
func (s *ReviewReminderService) OpenReminder(ctx context.Context, user, opening string) (*domain.EventBadge, error) {
	if _, err := uuid.Parse(opening); err != nil {
		return nil, domain.ErrInvalidSurvey
	}
	return s.repo.OpenReminder(ctx, user, opening)
}
