package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
	"time"
)

type SessionRepository interface {
	GetPasswordAndSessionVersion(context.Context, string) (string, int64, error)
	GetSessionVersion(context.Context, string) (int64, error)
}

type PasswordRecoveryRepository interface {
	InitSchema(context.Context) error
	AllowAttempt(context.Context, string, int, time.Duration) (bool, error)
	QueuePasswordReset(context.Context, string, string, string, time.Duration) error
	ClaimPasswordReset(context.Context) (*domain.PasswordResetDelivery, error)
	CompletePasswordResetDelivery(context.Context, string, bool) error
	FailPasswordResetDelivery(context.Context, string, int) error
	ResetPassword(context.Context, string, string) error
}

type PasswordResetSender interface {
	SendPasswordReset(ctx context.Context, email, link, deliveryID string) error
}

type PasswordRecoveryService interface {
	Enabled() bool
	RequestReset(ctx context.Context, email, clientIP string) error
	ResetPassword(ctx context.Context, token, password, confirmation, clientIP string) error
	ProcessNext(ctx context.Context) (bool, error)
}

type SessionValidator func(ctx context.Context, userID string, version int64) error
