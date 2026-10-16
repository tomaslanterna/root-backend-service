package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
)

type ReviewReminderRepository interface {
	Checkout(context.Context, string, string) error
	OpenReminder(context.Context, string, string) (*domain.EventBadge, error)
}
