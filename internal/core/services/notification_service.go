package services

import (
	"context"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"
)

type NotificationService struct {
	repo       ports.NotificationRepository
	pushRepo   ports.PushRepository
	pushSender ports.PushSender
}

func NewNotificationService(repo ports.NotificationRepository, pushRepo ports.PushRepository, sender ports.PushSender) *NotificationService {
	return &NotificationService{repo: repo, pushRepo: pushRepo, pushSender: sender}
}

func (s *NotificationService) Publish(ctx context.Context, input domain.NotificationInput) error {
	return s.repo.Create(ctx, input)
}

func (s *NotificationService) List(ctx context.Context, userID string, limit, offset int) (domain.NotificationPage, error) {
	return s.repo.List(ctx, userID, limit, offset)
}

func (s *NotificationService) MarkRead(ctx context.Context, userID, notificationID string) error {
	return s.repo.MarkRead(ctx, userID, notificationID)
}

func (s *NotificationService) MarkAllRead(ctx context.Context, userID string) error {
	return s.repo.MarkAllRead(ctx, userID)
}

func (s *NotificationService) GetPreferences(ctx context.Context, userID string) (domain.NotificationPreferences, error) {
	return s.repo.GetPreferences(ctx, userID)
}

func (s *NotificationService) UpdatePreferences(ctx context.Context, userID string, prefs domain.NotificationPreferences) error {
	return s.repo.UpdatePreferences(ctx, userID, prefs)
}

func (s *NotificationService) NotifyFollow(ctx context.Context, actorID, targetUserID string) error {
	return s.repo.NotifyFollow(ctx, actorID, targetUserID)
}

func (s *NotificationService) NotifyPostLike(ctx context.Context, actorID, postID string) error {
	return s.repo.NotifyPostLike(ctx, actorID, postID)
}

func (s *NotificationService) NotifyPostComment(ctx context.Context, actorID, postID, commentID string) error {
	return s.repo.NotifyPostComment(ctx, actorID, postID, commentID)
}

func (s *NotificationService) NotifyEventComment(ctx context.Context, actorID, eventID, commentID string) error {
	return s.repo.NotifyEventComment(ctx, actorID, eventID, commentID)
}

func (s *NotificationService) NotifyCommunityAnnouncement(ctx context.Context, actorID, communityID, postID string) error {
	return s.repo.NotifyCommunityAnnouncement(ctx, actorID, communityID, postID)
}

func (s *NotificationService) CancelEventReminders(ctx context.Context, userID, eventID string) error {
	return s.repo.CancelEventReminders(ctx, userID, eventID)
}

func (s *NotificationService) ScheduleEventReminders(ctx context.Context) error {
	return s.repo.ScheduleEventReminders(ctx)
}

// ErrInvalidBirthdayDate indica que no se pasó la fecha para buscar cumpleaños.
var ErrInvalidBirthdayDate = errors.New("birthday date is required")

// Uruguay no usa horario de verano desde 2015: si el host no trae la base de
// zonas horarias, UTC-3 fijo da la misma fecha.
var montevideoLocation = func() *time.Location {
	if location, err := time.LoadLocation("America/Montevideo"); err == nil {
		return location
	}
	return time.FixedZone("UYT", -3*60*60)
}()

// MontevideoDate devuelve la fecha de calendario de Montevideo de now, a medianoche de Montevideo.
func MontevideoDate(now time.Time) time.Time {
	year, month, day := now.In(montevideoLocation).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, montevideoLocation)
}

// ScheduleBirthdays agenda el saludo de quienes cumplen en today (fecha de
// Montevideo). El worker lo llama una vez por día; igual es idempotente (dedupe birthday:{año}).
func (s *NotificationService) ScheduleBirthdays(ctx context.Context, today time.Time) error {
	if today.IsZero() {
		return fmt.Errorf("schedule birthdays: %w", ErrInvalidBirthdayDate)
	}
	year, month, day := today.Date()
	return s.repo.ScheduleBirthdays(ctx, time.Date(year, month, day, 0, 0, 0, 0, time.UTC))
}

func (s *NotificationService) Prune(ctx context.Context) error { return s.repo.Prune(ctx) }

func (s *NotificationService) ProcessPending(ctx context.Context) error {
	if s.pushSender == nil {
		return nil
	}
	jobs, err := s.repo.ClaimDeliveries(ctx)
	if err != nil {
		return fmt.Errorf("claim notification deliveries: %w", err)
	}
	var failures []error
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		allowed, sendErr := s.repo.CanSendDelivery(attemptCtx, job)
		if sendErr == nil && allowed {
			sendErr = s.pushSender.Send(attemptCtx, job)
		}
		cancel()
		done := sendErr == nil
		if errors.Is(sendErr, domain.ErrPushTokenExpired) {
			if removeErr := s.pushRepo.ExpireToken(ctx, job); removeErr != nil {
				failures = append(failures, removeErr)
				done = false
			} else {
				done = true
			}
		} else if sendErr != nil {
			failures = append(failures, fmt.Errorf("notification delivery %d attempt %d: %w", job.ID, job.Attempt, sendErr))
		}
		retry := time.Duration(1<<job.Attempt) * 30 * time.Second
		if err := s.repo.FinishDelivery(ctx, job, done, retry); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
