package services

import (
	"context"
	"root-backend-service/internal/core/domain"
	"testing"
	"time"
)

type notificationRepoFake struct {
	jobs     []domain.PushJob
	finished []domain.PushJob
}

func (*notificationRepoFake) InitSchema(context.Context) error                       { return nil }
func (*notificationRepoFake) Create(context.Context, domain.NotificationInput) error { return nil }
func (*notificationRepoFake) List(context.Context, string, int, int) (domain.NotificationPage, error) {
	return domain.NotificationPage{}, nil
}
func (*notificationRepoFake) MarkRead(context.Context, string, string) error { return nil }
func (*notificationRepoFake) MarkAllRead(context.Context, string) error      { return nil }
func (*notificationRepoFake) GetPreferences(context.Context, string) (domain.NotificationPreferences, error) {
	return domain.NotificationPreferences{}, nil
}
func (*notificationRepoFake) UpdatePreferences(context.Context, string, domain.NotificationPreferences) error {
	return nil
}
func (r *notificationRepoFake) ClaimDeliveries(context.Context) ([]domain.PushJob, error) {
	return r.jobs, nil
}
func (*notificationRepoFake) CanSendDelivery(context.Context, domain.PushJob) (bool, error) {
	return true, nil
}
func (r *notificationRepoFake) FinishDelivery(_ context.Context, job domain.PushJob, _ bool, _ time.Duration) error {
	r.finished = append(r.finished, job)
	return nil
}
func (*notificationRepoFake) ScheduleEventReminders(context.Context) error         { return nil }
func (*notificationRepoFake) Prune(context.Context) error                          { return nil }
func (*notificationRepoFake) NotifyFollow(context.Context, string, string) error   { return nil }
func (*notificationRepoFake) NotifyPostLike(context.Context, string, string) error { return nil }
func (*notificationRepoFake) NotifyPostComment(context.Context, string, string, string) error {
	return nil
}
func (*notificationRepoFake) NotifyEventComment(context.Context, string, string, string) error {
	return nil
}
func (*notificationRepoFake) NotifyCommunityAnnouncement(context.Context, string, string, string) error {
	return nil
}
func (*notificationRepoFake) CancelEventReminders(context.Context, string, string) error { return nil }

type notificationPushRepoFake struct{}

func (*notificationPushRepoFake) RegisterDevice(context.Context, string, string, string, string) error {
	return nil
}
func (*notificationPushRepoFake) RemoveDevice(context.Context, string, string) error { return nil }
func (*notificationPushRepoFake) ClaimJobs(context.Context) ([]domain.PushJob, error) {
	return nil, nil
}
func (*notificationPushRepoFake) CanSend(context.Context, domain.PushJob) (bool, error) {
	return true, nil
}
func (*notificationPushRepoFake) FinishJob(context.Context, domain.PushJob, bool, time.Duration) error {
	return nil
}
func (*notificationPushRepoFake) ExpireToken(context.Context, domain.PushJob) error { return nil }

type notificationSenderFake struct{ sent []domain.PushJob }

func (s *notificationSenderFake) Send(_ context.Context, job domain.PushJob) error {
	s.sent = append(s.sent, job)
	return nil
}

func TestNotificationServiceProcessesGenericDelivery(t *testing.T) {
	repo := &notificationRepoFake{jobs: []domain.PushJob{{ID: 1, NotificationID: "n", NotificationType: "event.today", UserID: "u"}}}
	sender := &notificationSenderFake{}
	service := NewNotificationService(repo, &notificationPushRepoFake{}, sender)
	if err := service.ProcessPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].NotificationType != "event.today" {
		t.Fatalf("unexpected sends: %+v", sender.sent)
	}
	if len(repo.finished) != 1 {
		t.Fatalf("delivery was not finalized")
	}
}
