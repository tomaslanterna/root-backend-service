package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"testing"
	"time"
)

type pushRepoStub struct {
	jobs           []domain.PushJob
	allowed        bool
	eligibilityErr error
	finished       []bool
	retries        []time.Duration
	expired        int
}

func (r *pushRepoStub) RegisterDevice(context.Context, string, string, string) error { return nil }
func (r *pushRepoStub) RemoveDevice(context.Context, string, string) error           { return nil }
func (r *pushRepoStub) ClaimJobs(context.Context) ([]domain.PushJob, error)          { return r.jobs, nil }
func (r *pushRepoStub) CanSend(context.Context, domain.PushJob) (bool, error) {
	return r.allowed, r.eligibilityErr
}
func (r *pushRepoStub) FinishJob(_ context.Context, _ domain.PushJob, done bool, retry time.Duration) error {
	r.finished = append(r.finished, done)
	r.retries = append(r.retries, retry)
	return nil
}
func (r *pushRepoStub) ExpireToken(context.Context, domain.PushJob) error { r.expired++; return nil }

type pushSenderStub struct {
	calls int
	err   error
}

func (s *pushSenderStub) Send(context.Context, domain.PushJob) error { s.calls++; return s.err }

func TestPushWorker(t *testing.T) {
	for _, test := range []struct {
		name                 string
		allowed              bool
		sendErr, eligibleErr error
		sends, expired       int
		done, wantErr        bool
	}{
		{"success", true, nil, nil, 1, 0, true, false},
		{"already read or revoked", false, nil, nil, 0, 0, true, false},
		{"retry provider outage", true, errors.New("unavailable"), nil, 1, 0, false, true},
		{"expired token", true, domain.ErrPushTokenExpired, nil, 1, 1, true, false},
		{"retry database outage", true, nil, errors.New("database unavailable"), 0, 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &pushRepoStub{jobs: []domain.PushJob{{ID: 1, Attempt: 2}}, allowed: test.allowed, eligibilityErr: test.eligibleErr}
			sender := &pushSenderStub{err: test.sendErr}
			err := NewPushService(repo, sender).ProcessPending(context.Background())
			if (err != nil) != test.wantErr || sender.calls != test.sends || repo.expired != test.expired || len(repo.finished) != 1 || repo.finished[0] != test.done {
				t.Fatalf("err=%v sends=%d expired=%d done=%v", err, sender.calls, repo.expired, repo.finished)
			}
			if repo.retries[0] != 2*time.Minute {
				t.Fatalf("retry=%v", repo.retries)
			}
		})
	}
}
func TestPushDisabled(t *testing.T) {
	service := NewPushService(&pushRepoStub{}, nil)
	if service.Enabled() || service.ProcessPending(context.Background()) != nil {
		t.Fatal("disabled service must be a no-op")
	}
}
