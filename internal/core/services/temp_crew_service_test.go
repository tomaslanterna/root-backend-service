package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type fakeTempCrewRepo struct {
	ports.TempCrewRepository
	created    *domain.CreateTempCrewInput
	joinMsg    *string
	limit      int
	status     string
	listEvents int
}

func (f *fakeTempCrewRepo) Create(_ context.Context, _, _ string, in domain.CreateTempCrewInput) (string, error) {
	f.created = &in
	return "crew", nil
}
func (f *fakeTempCrewRepo) Summary(context.Context, string, string) (*domain.TempCrewSummary, error) {
	return &domain.TempCrewSummary{ID: "crew"}, nil
}
func (f *fakeTempCrewRepo) RequestJoin(_ context.Context, _, _ string, msg *string) (domain.TempCrewJoinRequest, bool, error) {
	f.joinMsg = msg
	return domain.TempCrewJoinRequest{}, true, nil
}
func (f *fakeTempCrewRepo) ListForEvent(_ context.Context, _, _ string, limit, _ int) (domain.TempCrewListPage, error) {
	f.limit = limit
	return domain.TempCrewListPage{}, nil
}
func (f *fakeTempCrewRepo) EventSummary(_ context.Context, _, _ string, limit int) (*domain.TempCrewsEventSummary, error) {
	f.limit = limit
	return &domain.TempCrewsEventSummary{Preview: []domain.TempCrewSummary{}}, nil
}
func (f *fakeTempCrewRepo) ListRequests(_ context.Context, _, _, status string, _, _ int) ([]domain.TempCrewJoinRequest, error) {
	f.status = status
	return nil, nil
}

func TestTempCrewCreateValidation(t *testing.T) {
	event := uuid.NewString()
	valid := domain.CreateTempCrewInput{Name: "  Los del fondo ", Capacity: 5, Plan: " Previa a las 22 "}
	cases := map[string]domain.CreateTempCrewInput{
		"empty name":      {Name: "   ", Capacity: 5, Plan: "plan"},
		"long name":       {Name: strings.Repeat("a", 41), Capacity: 5, Plan: "plan"},
		"empty plan":      {Name: "n", Capacity: 5, Plan: " "},
		"long plan":       {Name: "n", Capacity: 5, Plan: strings.Repeat("a", 281)},
		"capacity below":  {Name: "n", Capacity: 1, Plan: "plan"},
		"capacity above":  {Name: "n", Capacity: 11, Plan: "plan"},
		"capacity absent": {Name: "n", Plan: "plan"},
	}
	for name, input := range cases {
		repo := &fakeTempCrewRepo{}
		var validation *domain.ValidationError
		if _, err := NewTempCrewService(repo).Create(context.Background(), "host", event, input); !errors.As(err, &validation) || repo.created != nil {
			t.Errorf("%s: expected a validation error without touching the repository, got %v", name, err)
		}
	}
	repo := &fakeTempCrewRepo{}
	if _, err := NewTempCrewService(repo).Create(context.Background(), "host", event, valid); err != nil {
		t.Fatal(err)
	}
	if repo.created.Name != "Los del fondo" || repo.created.Plan != "Previa a las 22" {
		t.Fatalf("name and plan are trimmed: %+v", repo.created)
	}
	// Los límites exactos son válidos (en caracteres, no en bytes).
	edge := domain.CreateTempCrewInput{Name: strings.Repeat("ñ", 40), Capacity: 10, Plan: strings.Repeat("ñ", 280)}
	if _, err := NewTempCrewService(&fakeTempCrewRepo{}).Create(context.Background(), "host", event, edge); err != nil {
		t.Fatalf("limits are inclusive and counted in runes: %v", err)
	}
	if _, err := NewTempCrewService(&fakeTempCrewRepo{}).Create(context.Background(), "host", "not-a-uuid", valid); !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("invalid event id: %v", err)
	}
}

func TestTempCrewRequestJoinMessage(t *testing.T) {
	crew := uuid.NewString()
	repo := &fakeTempCrewRepo{}
	svc := NewTempCrewService(repo)
	if _, _, err := svc.RequestJoin(context.Background(), crew, "u", "   "); err != nil || repo.joinMsg != nil {
		t.Fatalf("a blank message is no message: %v %v", err, repo.joinMsg)
	}
	if _, _, err := svc.RequestJoin(context.Background(), crew, "u", " Voy solo "); err != nil || repo.joinMsg == nil || *repo.joinMsg != "Voy solo" {
		t.Fatalf("message is trimmed: %v", repo.joinMsg)
	}
	var validation *domain.ValidationError
	if _, _, err := svc.RequestJoin(context.Background(), crew, "u", strings.Repeat("a", 281)); !errors.As(err, &validation) {
		t.Fatalf("long message: %v", err)
	}
	if _, _, err := svc.RequestJoin(context.Background(), "x", "u", ""); !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("invalid crew id: %v", err)
	}
}

func TestTempCrewListDefaults(t *testing.T) {
	repo := &fakeTempCrewRepo{}
	svc := NewTempCrewService(repo)
	event := uuid.NewString()
	for input, want := range map[int]int{0: 20, -3: 20, 7: 7, 50: 50, 500: 50} {
		if _, err := svc.ListForEvent(context.Background(), event, "", input, 0); err != nil || repo.limit != want {
			t.Errorf("limit %d → %d, got %d (%v)", input, want, repo.limit, err)
		}
	}
	if _, err := svc.ListRequests(context.Background(), event, "host", "", 0, 0); err != nil || repo.status != "pending" {
		t.Fatalf("status defaults to pending: %q %v", repo.status, err)
	}
	var validation *domain.ValidationError
	if _, err := svc.ListRequests(context.Background(), event, "host", "accepted", 0, 0); !errors.As(err, &validation) {
		t.Fatalf("only pending and rejected are listable: %v", err)
	}
}

func TestTempCrewEventSummaryAsksForThreeAndValidatesTheID(t *testing.T) {
	repo := &fakeTempCrewRepo{}
	svc := NewTempCrewService(repo)
	if _, err := svc.EventSummary(context.Background(), uuid.NewString(), "viewer"); err != nil || repo.limit != 3 {
		t.Fatalf("the event detail previews three crews: limit=%d err=%v", repo.limit, err)
	}
	if _, err := svc.EventSummary(context.Background(), "not-a-uuid", ""); !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("invalid event id: %v", err)
	}
}
