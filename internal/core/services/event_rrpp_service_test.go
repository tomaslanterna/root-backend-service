package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
)

type fakeEventRRPPRepo struct {
	listed   []domain.EventRRPP
	assigned []int
	calls    int
	err      error
}

func (f *fakeEventRRPPRepo) InitSchema(context.Context) error { return nil }
func (f *fakeEventRRPPRepo) ListForEvent(context.Context, string) ([]domain.EventRRPP, error) {
	f.calls++
	return f.listed, f.err
}
func (f *fakeEventRRPPRepo) Assign(_ context.Context, _, _ string, position int) error {
	f.calls++
	f.assigned = append(f.assigned, position)
	return f.err
}
func (f *fakeEventRRPPRepo) Remove(context.Context, string, string) (bool, error) {
	f.calls++
	return true, f.err
}

func TestEventRRPPAssignValidation(t *testing.T) {
	event, user := uuid.NewString(), uuid.NewString()
	cases := []struct {
		name     string
		event    string
		user     string
		position int
		want     error
	}{
		{"valid", event, user, 1, nil},
		{"position zero", event, user, 0, nil},
		{"max position", event, user, domain.EventRRPPMaxPosition, nil},
		{"negative position", event, user, -1, domain.ErrInvalidRRPPPosition},
		{"position over smallint", event, user, domain.EventRRPPMaxPosition + 1, domain.ErrInvalidRRPPPosition},
		{"invalid event", "event-1", user, 1, domain.ErrInvalidID},
		{"invalid user", event, "", 1, domain.ErrInvalidID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeEventRRPPRepo{}
			err := NewEventRRPPService(repo).Assign(context.Background(), tc.event, tc.user, tc.position)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.want != nil && repo.calls != 0 {
				t.Fatal("invalid input must not reach the repository")
			}
			if tc.want == nil && (len(repo.assigned) != 1 || repo.assigned[0] != tc.position) {
				t.Fatalf("position not forwarded: %v", repo.assigned)
			}
		})
	}
}

func TestEventRRPPAssignPropagatesRepositoryErrors(t *testing.T) {
	for _, want := range []error{domain.ErrNotRRPP, domain.ErrUserNotFound, domain.ErrEventNotFound} {
		repo := &fakeEventRRPPRepo{err: want}
		if err := NewEventRRPPService(repo).Assign(context.Background(), uuid.NewString(), uuid.NewString(), 1); !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
	}
}

func TestEventRRPPListNeverReturnsNil(t *testing.T) {
	repo := &fakeEventRRPPRepo{listed: nil}
	rrpps, err := NewEventRRPPService(repo).ListForEvent(context.Background(), uuid.NewString())
	if err != nil || rrpps == nil || len(rrpps) != 0 {
		t.Fatalf("expected an empty non-nil slice, got %#v (%v)", rrpps, err)
	}
	if _, err := NewEventRRPPService(repo).ListForEvent(context.Background(), "not-a-uuid"); !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("invalid event id: %v", err)
	}
	repo.err = errors.New("db down")
	if _, err := NewEventRRPPService(repo).ListForEvent(context.Background(), uuid.NewString()); err == nil {
		t.Fatal("repository errors must be returned")
	}
}

func TestEventRRPPRemoveValidatesIDs(t *testing.T) {
	repo := &fakeEventRRPPRepo{}
	if _, err := NewEventRRPPService(repo).Remove(context.Background(), uuid.NewString(), "x"); !errors.Is(err, domain.ErrInvalidID) || repo.calls != 0 {
		t.Fatalf("got %v (calls=%d)", err, repo.calls)
	}
	removed, err := NewEventRRPPService(repo).Remove(context.Background(), uuid.NewString(), uuid.NewString())
	if err != nil || !removed {
		t.Fatalf("got %v %v", removed, err)
	}
}
