package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
)

type BadgeRepository interface {
	List(context.Context, string, string, int, int) ([]domain.EventBadge, int, error)
	Get(context.Context, string, string) (*domain.EventBadge, error)
}
