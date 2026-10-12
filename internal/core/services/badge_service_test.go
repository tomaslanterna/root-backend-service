package services

import (
	"context"
	"errors"
	"testing"

	"root-backend-service/internal/core/domain"
)

type badgeRepoStub struct {
	username, viewer, event string
	limit, offset           int
	err                     error
}

func (s *badgeRepoStub) List(_ context.Context, username, viewer string, limit, offset int) ([]domain.EventBadge, int, error) {
	s.username, s.viewer, s.limit, s.offset = username, viewer, limit, offset
	return []domain.EventBadge{{EventID: "event"}}, 9, s.err
}
func (s *badgeRepoStub) Get(_ context.Context, viewer, event string) (*domain.EventBadge, error) {
	s.viewer, s.event = viewer, event
	return &domain.EventBadge{EventID: event}, s.err
}
func TestBadgeServicePreservesViewerAndRepositoryResults(t *testing.T) {
	repo := &badgeRepoStub{}
	service := NewBadgeService(repo)
	page, total, err := service.List(context.Background(), "profile-owner", "viewer", 12, 24)
	if err != nil || total != 9 || len(page) != 1 || repo.username != "profile-owner" || repo.viewer != "viewer" || repo.limit != 12 || repo.offset != 24 {
		t.Fatal("lost list identity or results")
	}
	badge, err := service.Get(context.Background(), "owner", "event")
	if err != nil || badge.EventID != "event" || repo.viewer != "owner" {
		t.Fatal("lost own badge identity")
	}
	repo.err = errors.New("database error")
	if _, _, err := service.List(context.Background(), "owner", "", 12, 0); !errors.Is(err, repo.err) {
		t.Fatal("list swallowed repository error")
	}
	if _, err := service.Get(context.Background(), "owner", "event"); !errors.Is(err, repo.err) {
		t.Fatal("detail swallowed repository error")
	}
}
