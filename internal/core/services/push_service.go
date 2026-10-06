package services

import (
	"context"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"
)

type PushService struct {
	repo   ports.PushRepository
	sender ports.PushSender
}

func NewPushService(repo ports.PushRepository, sender ports.PushSender) *PushService {
	return &PushService{repo: repo, sender: sender}
}
func (s *PushService) Enabled() bool { return s.sender != nil }
func (s *PushService) RegisterDevice(ctx context.Context, userID, id, token string) error {
	return s.repo.RegisterDevice(ctx, userID, id, token)
}
func (s *PushService) RemoveDevice(ctx context.Context, userID, id string) error {
	return s.repo.RemoveDevice(ctx, userID, id)
}

// Persistent leases allow multiple API processes to share this queue safely.
// Acceptance by FCM never changes a chat message's delivered/read receipts.
func (s *PushService) ProcessPending(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	jobs, err := s.repo.ClaimJobs(ctx)
	if err != nil {
		return fmt.Errorf("claim push jobs: %w", err)
	}
	var failures []error
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		allowed, err := s.repo.CanSend(attemptCtx, job)
		if err == nil && allowed {
			err = s.sender.Send(attemptCtx, job)
		}
		cancel()
		done := err == nil
		if errors.Is(err, domain.ErrPushTokenExpired) {
			if removeErr := s.repo.ExpireToken(ctx, job); removeErr != nil {
				failures = append(failures, removeErr)
				done = false
			} else {
				done = true
			}
		} else if err != nil {
			failures = append(failures, fmt.Errorf("push job %d attempt %d: %w", job.ID, job.Attempt, err))
		}
		retry := time.Duration(1<<job.Attempt) * 30 * time.Second
		if err := s.repo.FinishJob(ctx, job, done, retry); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
