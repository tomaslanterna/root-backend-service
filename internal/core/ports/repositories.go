package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
)

type KycRepository interface {
	CreateSession(ctx context.Context, session *domain.KycSession) error
	GetSessionByID(ctx context.Context, id string) (*domain.KycSession, error)
	UpdateSession(ctx context.Context, session *domain.KycSession) error
	InitSchema(ctx context.Context) error
}
