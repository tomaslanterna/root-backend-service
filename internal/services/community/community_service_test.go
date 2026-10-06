package community

import (
	"context"
	"root-backend-service/internal/core/domain"
	"testing"
)

type communityRepositoryStub struct {
	filter       domain.CommunityFilter
	joinCalls    int
	leaveCalls   int
	currentUser  string
	membersCount int
}

func (s *communityRepositoryStub) InitSchema(context.Context) error { return nil }
func (s *communityRepositoryStub) GetCommunities(_ context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	s.filter = filter
	s.currentUser = currentUserID
	return []domain.Community{}, 0, nil
}
func (s *communityRepositoryStub) GetCommunityByIDOrSlug(context.Context, string, string) (*domain.Community, error) {
	return &domain.Community{ID: "community-1"}, nil
}
func (s *communityRepositoryStub) JoinCommunity(context.Context, string, string) (int, error) {
	s.joinCalls++
	return s.membersCount, nil
}
func (s *communityRepositoryStub) LeaveCommunity(context.Context, string, string) (int, error) {
	s.leaveCalls++
	return s.membersCount, nil
}
func (s *communityRepositoryStub) GetUserCommunities(context.Context, string, string) ([]domain.Community, error) {
	return []domain.Community{}, nil
}
func (s *communityRepositoryStub) GetCurrentUserCommunities(context.Context, string) ([]domain.Community, error) {
	return []domain.Community{}, nil
}
func (s *communityRepositoryStub) CanPublish(context.Context, string, string) (bool, bool, error) {
	return true, false, nil
}

func TestGetCommunitiesNormalizesFiltersAndPagination(t *testing.T) {
	repository := &communityRepositoryStub{}
	service := NewCommunityService(repository)

	_, _, err := service.GetCommunities(context.Background(), domain.CommunityFilter{
		Country: " UY ", Category: " electrónica ", Department: " Montevideo ", Query: " fiestas ", Limit: 99,
	}, "viewer-1")
	if err != nil {
		t.Fatalf("GetCommunities returned an error: %v", err)
	}
	if repository.filter.Country != "UY" || repository.filter.Category != "electrónica" || repository.filter.Department != "Montevideo" || repository.filter.Query != "fiestas" {
		t.Fatalf("filters were not normalized: %+v", repository.filter)
	}
	if repository.filter.Limit != 50 || repository.filter.Offset != 0 {
		t.Fatalf("pagination was not bounded: %+v", repository.filter)
	}
	if repository.currentUser != "viewer-1" {
		t.Fatalf("current user was not forwarded")
	}
}

func TestJoinAndLeaveAreSeparateIdempotentOperations(t *testing.T) {
	repository := &communityRepositoryStub{membersCount: 4}
	service := NewCommunityService(repository)

	if _, err := service.JoinCommunity(context.Background(), "community-1", "user-1"); err != nil {
		t.Fatalf("join returned an error: %v", err)
	}
	if _, err := service.JoinCommunity(context.Background(), "community-1", "user-1"); err != nil {
		t.Fatalf("second join returned an error: %v", err)
	}
	if _, err := service.LeaveCommunity(context.Background(), "community-1", "user-1"); err != nil {
		t.Fatalf("leave returned an error: %v", err)
	}

	if repository.joinCalls != 2 || repository.leaveCalls != 1 {
		t.Fatalf("unexpected operation calls: joins=%d leaves=%d", repository.joinCalls, repository.leaveCalls)
	}
}
