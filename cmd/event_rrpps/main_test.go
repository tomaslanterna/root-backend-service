package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type fakeRRPPService struct {
	ports.EventRRPPService
	event, user string
	position    int
	action      string
	list        []domain.EventRRPP
}

func (f *fakeRRPPService) Assign(_ context.Context, event, user string, position int) error {
	f.action, f.event, f.user, f.position = "assign", event, user, position
	return nil
}
func (f *fakeRRPPService) Remove(_ context.Context, event, user string) (bool, error) {
	f.action, f.event, f.user = "remove", event, user
	return true, nil
}
func (f *fakeRRPPService) ListForEvent(_ context.Context, event string) ([]domain.EventRRPP, error) {
	f.action, f.event = "list", event
	return f.list, nil
}

type fakeUsers struct{ asked string }

func (f *fakeUsers) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	f.asked = username
	if username != "valesosa" {
		return nil, domain.ErrUserNotFound
	}
	return &domain.User{ID: "user-from-alias"}, nil
}

func TestRunDispatchesCommands(t *testing.T) {
	cases := []struct {
		name              string
		args              []string
		action, user, out string
		position          int
	}{
		{"assign by id", []string{"assign", "--event", "e1", "--user", "u1", "--position", "3"}, "assign", "u1", "assigned", 3},
		{"assign by username is lowercased", []string{"assign", "--event", "e1", "--username", " ValeSosa ", "--position", "1"}, "assign", "user-from-alias", "assigned", 1},
		{"assign default position", []string{"assign", "--event", "e1", "--user", "u1"}, "assign", "u1", "position 0", 0},
		{"remove", []string{"remove", "--event", "e1", "--user", "u1"}, "remove", "u1", "removed", 0},
		{"list empty", []string{"list", "--event", "e1"}, "list", "", "no RRPPs", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, users, out := &fakeRRPPService{}, &fakeUsers{}, &bytes.Buffer{}
			if err := run(context.Background(), tc.args, svc, users, out); err != nil {
				t.Fatal(err)
			}
			if svc.action != tc.action || svc.event != "e1" || svc.user != tc.user || svc.position != tc.position {
				t.Fatalf("unexpected call: %+v", svc)
			}
			if !strings.Contains(out.String(), tc.out) {
				t.Fatalf("output %q does not mention %q", out.String(), tc.out)
			}
		})
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"no command":           nil,
		"unknown command":      {"promote", "--event", "e1", "--user", "u1"},
		"missing event":        {"assign", "--user", "u1"},
		"missing user":         {"assign", "--event", "e1"},
		"user and username":    {"assign", "--event", "e1", "--user", "u1", "--username", "valesosa"},
		"unknown username":     {"assign", "--event", "e1", "--username", "nadie"},
		"stray argument":       {"assign", "--event", "e1", "--user", "u1", "extra"},
		"non-numeric position": {"assign", "--event", "e1", "--user", "u1", "--position", "uno"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeRRPPService{}
			if err := run(context.Background(), args, svc, &fakeUsers{}, &bytes.Buffer{}); err == nil {
				t.Fatal("expected an error")
			}
			if svc.action != "" {
				t.Fatalf("bad input must not reach the service: %+v", svc)
			}
		})
	}
}
