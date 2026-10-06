package services

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"testing"
)

type matcherRepositoryStub struct {
	calls   int
	failure error
	crew    *domain.MatcherSquad
}

func (r *matcherRepositoryStub) ProcessSwipe(_ context.Context, swipe *domain.EventSwipe) (*domain.MatcherSquad, error) {
	r.calls++
	if swipe.ID == "" || swipe.CreatedAt.IsZero() {
		return nil, errors.New("missing metadata")
	}
	return r.crew, r.failure
}
func (r *matcherRepositoryStub) GetUserSquads(context.Context, string) ([]domain.MatcherSquad, error) {
	return nil, r.failure
}
func (r *matcherRepositoryStub) EnsureSquadChat(context.Context, string, string) (string, error) {
	r.calls++
	return "chat", r.failure
}

func TestMatcherValidationAndAtomicRepository(t *testing.T) {
	repo := &matcherRepositoryStub{}
	service := NewMatchService(repo)
	for _, direction := range []string{"", "no", "LIKE"} {
		_, err := service.ProcessSwipe(context.Background(), &domain.EventSwipe{EventID: uuid.NewString(), UserID: uuid.NewString(), Direction: direction})
		if !errors.Is(err, domain.ErrInvalidSwipe) || repo.calls != 0 {
			t.Fatal("invalid swipe reached repository")
		}
	}
	for _, direction := range []string{"like", "superlike", "pass"} {
		result, err := service.ProcessSwipe(context.Background(), &domain.EventSwipe{EventID: uuid.NewString(), UserID: uuid.NewString(), Direction: direction})
		if err != nil || result["status"] != "queued" {
			t.Fatalf("valid %s: %v %v", direction, result, err)
		}
	}
	failure := errors.New("atomic transaction failed")
	repo.failure = failure
	if _, err := service.ProcessSwipe(context.Background(), &domain.EventSwipe{EventID: uuid.NewString(), UserID: uuid.NewString(), Direction: "like"}); !errors.Is(err, failure) {
		t.Fatal("failure was swallowed")
	}
	repo.failure = nil
	repo.crew = &domain.MatcherSquad{ID: uuid.NewString(), ChatRoomID: uuid.NewString(), Members: []domain.MatcherSquadMember{{UserID: uuid.NewString()}}}
	result, err := service.ProcessSwipe(context.Background(), &domain.EventSwipe{EventID: uuid.NewString(), UserID: uuid.NewString(), Direction: "like"})
	if err != nil || result["crew"] != repo.crew {
		t.Fatal("missing real member/chat DTO")
	}
	if _, err := service.EnsureSquadChat(context.Background(), "sq1", uuid.NewString()); !errors.Is(err, domain.ErrInvalidSwipe) {
		t.Fatal("accepted mock squad ID")
	}
}
