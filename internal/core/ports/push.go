package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
	"time"
)

type PushRepository interface {
	RegisterDevice(context.Context, string, string, string) error
	RemoveDevice(context.Context, string, string) error
	ClaimJobs(context.Context) ([]domain.PushJob, error)
	CanSend(context.Context, domain.PushJob) (bool, error)
	FinishJob(context.Context, domain.PushJob, bool, time.Duration) error
	ExpireToken(context.Context, domain.PushJob) error
}

type PushSender interface {
	Send(context.Context, domain.PushJob) error
}
