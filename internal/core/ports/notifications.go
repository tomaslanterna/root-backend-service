package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
	"time"
)

type NotificationRepository interface {
	InitSchema(context.Context) error
	Create(context.Context, domain.NotificationInput) error
	List(context.Context, string, int, int) (domain.NotificationPage, error)
	MarkRead(context.Context, string, string) error
	MarkAllRead(context.Context, string) error
	GetPreferences(context.Context, string) (domain.NotificationPreferences, error)
	UpdatePreferences(context.Context, string, domain.NotificationPreferences) error
	ClaimDeliveries(context.Context) ([]domain.PushJob, error)
	CanSendDelivery(context.Context, domain.PushJob) (bool, error)
	FinishDelivery(context.Context, domain.PushJob, bool, time.Duration) error
	ScheduleEventReminders(context.Context) error
	// ScheduleBirthdays recibe la fecha de calendario de Montevideo (solo importan año, mes y día).
	ScheduleBirthdays(context.Context, time.Time) error
	Prune(context.Context) error
	NotifyFollow(context.Context, string, string) error
	NotifyPostLike(context.Context, string, string) error
	NotifyPostComment(context.Context, string, string, string) error
	NotifyEventComment(context.Context, string, string, string) error
	NotifyCommunityAnnouncement(context.Context, string, string, string) error
	CancelEventReminders(context.Context, string, string) error
}

type NotificationPublisher interface {
	Publish(context.Context, domain.NotificationInput) error
	NotifyFollow(context.Context, string, string) error
	NotifyPostLike(context.Context, string, string) error
	NotifyPostComment(context.Context, string, string, string) error
	NotifyEventComment(context.Context, string, string, string) error
	NotifyCommunityAnnouncement(context.Context, string, string, string) error
	CancelEventReminders(context.Context, string, string) error
}
