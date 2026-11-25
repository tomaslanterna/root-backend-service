package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"testing"
	"time"
)

type notificationRepoFake struct {
	jobs          []domain.PushJob
	finished      []domain.PushJob
	birthdayDates []time.Time
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
func (*notificationRepoFake) ScheduleEventReminders(context.Context) error { return nil }
func (r *notificationRepoFake) ScheduleBirthdays(_ context.Context, today time.Time) error {
	r.birthdayDates = append(r.birthdayDates, today)
	return nil
}
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

func TestMontevideoDate(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"UTC after midnight is still the previous day in Montevideo", time.Date(2026, time.November, 26, 2, 59, 0, 0, time.UTC), "2026-11-25"},
		{"03:00 UTC is midnight in Montevideo", time.Date(2026, time.November, 26, 3, 0, 0, 0, time.UTC), "2026-11-26"},
		{"new year in Montevideo", time.Date(2027, time.January, 1, 3, 30, 0, 0, time.UTC), "2027-01-01"},
		{"late night in Montevideo", time.Date(2026, time.February, 28, 23, 59, 0, 0, montevideoLocation), "2026-02-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MontevideoDate(tt.now)
			if got.Format("2006-01-02") != tt.want || got.Hour() != 0 || got.Minute() != 0 {
				t.Fatalf("MontevideoDate(%s) = %s, want %s at midnight", tt.now, got, tt.want)
			}
		})
	}
}

func TestNotificationServiceScheduleBirthdays(t *testing.T) {
	tests := []struct {
		name    string
		today   time.Time
		wantErr error
		want    string
	}{
		{"zero date is rejected", time.Time{}, ErrInvalidBirthdayDate, ""},
		{"keeps the Montevideo calendar date", time.Date(2026, time.November, 25, 23, 30, 0, 0, montevideoLocation), nil, "2026-11-25"},
		{"keeps the calendar date of a UTC value", time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC), nil, "2026-02-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &notificationRepoFake{}
			err := NewNotificationService(repo, &notificationPushRepoFake{}, nil).ScheduleBirthdays(context.Background(), tt.today)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if len(repo.birthdayDates) != 0 {
					t.Fatalf("repository must not be called on invalid input: %v", repo.birthdayDates)
				}
				return
			}
			if len(repo.birthdayDates) != 1 || repo.birthdayDates[0].Format("2006-01-02") != tt.want {
				t.Fatalf("repository got %v, want %s", repo.birthdayDates, tt.want)
			}
		})
	}
}
