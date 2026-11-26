package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

const reportID = "6f1c1d2e-3b4a-4c5d-8e9f-0a1b2c3d4e5f"

type reportsFake struct {
	ports.ReportRepository
	report    domain.ContentReport
	resolved  string
	removed   bool
	suspended string
}

func (f *reportsFake) GetReport(context.Context, string) (*domain.ContentReport, error) {
	r := f.report
	return &r, nil
}
func (f *reportsFake) ResolveReport(_ context.Context, _ string, status string) error {
	f.resolved = status
	return nil
}
func (f *reportsFake) RemoveReportedContent(context.Context, domain.ContentReport) error {
	f.removed = true
	return nil
}
func (f *reportsFake) SuspendUser(_ context.Context, userID string) error {
	f.suspended = userID
	return nil
}

type usersFake struct{}

func (usersFake) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	if username == "beto" {
		return &domain.User{ID: "beto-id"}, nil
	}
	return nil, domain.ErrUserNotFound
}

func TestRemoveHidesContentAndActionsReport(t *testing.T) {
	reports := &reportsFake{report: domain.ContentReport{ID: reportID, TargetType: domain.ReportTargetComment, TargetID: "c"}}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"remove", "--id", reportID}, reports, usersFake{}, &out); err != nil {
		t.Fatal(err)
	}
	if !reports.removed || reports.resolved != domain.ReportStatusActioned {
		t.Fatalf("removed=%v resolved=%q", reports.removed, reports.resolved)
	}
}

func TestRemoveRefusesUserReports(t *testing.T) {
	reports := &reportsFake{report: domain.ContentReport{ID: reportID, TargetType: domain.ReportTargetUser, TargetID: "u"}}
	err := run(context.Background(), []string{"remove", "--id", reportID}, reports, usersFake{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "suspend") || reports.removed {
		t.Fatalf("err=%v removed=%v", err, reports.removed)
	}
}

func TestSuspendByUsernameAndResolve(t *testing.T) {
	reports := &reportsFake{}
	if err := run(context.Background(), []string{"suspend", "--user", "@Beto", "--id", reportID}, reports, usersFake{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if reports.suspended != "beto-id" || reports.resolved != domain.ReportStatusActioned {
		t.Fatalf("suspended=%q resolved=%q", reports.suspended, reports.resolved)
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"show", "--id", "nope"}, {"suspend"}, {"wat"}, {"list", "--limit", "0"}} {
		if err := run(context.Background(), args, &reportsFake{}, usersFake{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %v accepted", args)
		}
	}
}
