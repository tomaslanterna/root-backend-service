package community

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"testing"
)

type experienceRepositoryStub struct {
	ports.CommunityRepository
	community domain.Community
	calls     int
	userID    string
	input     domain.CommunityReportInput
}

func (r *experienceRepositoryStub) GetCommunityByIDOrSlug(context.Context, string, string) (*domain.Community, error) {
	return &r.community, nil
}
func (r *experienceRepositoryStub) SetMuted(_ context.Context, _, userID string, _ bool) error {
	r.calls++
	r.userID = userID
	return nil
}
func (r *experienceRepositoryStub) MarkRead(_ context.Context, _, userID, _ string) error {
	r.calls++
	r.userID = userID
	return nil
}
func (r *experienceRepositoryStub) SetPinned(context.Context, string, string, bool) error {
	r.calls++
	return nil
}
func (r *experienceRepositoryStub) CreateReport(_ context.Context, _, userID string, input domain.CommunityReportInput) (string, error) {
	r.calls++
	r.userID = userID
	r.input = input
	return "report", nil
}
func (r *experienceRepositoryStub) GetReports(context.Context, string, int, int) ([]domain.CommunityReport, int, error) {
	r.calls++
	return []domain.CommunityReport{}, 0, nil
}
func (r *experienceRepositoryStub) ReviewReport(context.Context, string, string, string, string) error {
	r.calls++
	return nil
}

func TestCommunityExperienceRequiresMembershipOrManagement(t *testing.T) {
	ctx := context.Background()
	repo := &experienceRepositoryStub{community: domain.Community{ID: uuid.NewString()}}
	service := NewCommunityService(repo)
	target := uuid.NewString()
	checks := []func() error{
		func() error { return service.SetMuted(ctx, "slug", "viewer", true) },
		func() error { return service.MarkRead(ctx, "slug", "viewer", target) },
		func() error { return service.SetPinned(ctx, "slug", "viewer", target, true) },
		func() error { _, _, err := service.GetReports(ctx, "slug", "viewer", 10, 0); return err },
		func() error { return service.ReviewReport(ctx, "slug", "viewer", target, "reviewed") },
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, domain.ErrCommunityForbidden) {
			t.Fatalf("operation %d authorization: %v", i, err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("unauthorized operation reached persistence")
	}
	repo.community.IsMember = true
	if err := service.SetMuted(ctx, "slug", "jwt-user", true); err != nil || repo.userID != "jwt-user" {
		t.Fatalf("membership mute: %v", err)
	}
	repo.community.CanPublish = true
	if err := service.SetPinned(ctx, "slug", "jwt-user", target, true); err != nil {
		t.Fatal(err)
	}
}

func TestReportValidationAndTrimming(t *testing.T) {
	ctx := context.Background()
	repo := &experienceRepositoryStub{community: domain.Community{ID: uuid.NewString()}}
	service := NewCommunityService(repo)
	valid := domain.CommunityReportInput{TargetID: uuid.NewString(), TargetType: "comment", Reason: "abuse", Details: "  explicación  "}
	if _, err := service.CreateReport(ctx, "slug", "jwt-user", valid); err != nil || repo.input.Details != "explicación" || repo.userID != "jwt-user" {
		t.Fatalf("valid report: %+v %v", repo.input, err)
	}
	for _, field := range []string{"id", "type", "reason", "length"} {
		bad := valid
		switch field {
		case "id":
			bad.TargetID = "invalid"
		case "type":
			bad.TargetType = "event"
		case "reason":
			bad.Reason = "invalid"
		case "length":
			bad.Details = strings.Repeat("á", 1001)
		}
		if _, err := service.CreateReport(ctx, "slug", "viewer", bad); !errors.Is(err, domain.ErrCommunityInvalid) {
			t.Fatalf("%s validation: %v", field, err)
		}
	}
	if repo.calls != 1 {
		t.Fatal("invalid reports reached persistence")
	}
	if _, _, err := service.GetReports(ctx, "slug", "viewer", 51, 0); !errors.Is(err, domain.ErrCommunityInvalid) {
		t.Fatalf("pagination validation: %v", err)
	}
	if err := service.ReviewReport(ctx, "slug", "viewer", valid.TargetID, "delete"); !errors.Is(err, domain.ErrCommunityInvalid) {
		t.Fatalf("review validation: %v", err)
	}
}
