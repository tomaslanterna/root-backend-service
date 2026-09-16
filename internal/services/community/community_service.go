package community

import (
	"context"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type communityService struct {
	repo ports.CommunityRepository
}

func NewCommunityService(repo ports.CommunityRepository) ports.CommunityService {
	return &communityService{
		repo: repo,
	}
}

func (s *communityService) GetCommunitiesByCountry(ctx context.Context, countryID string, currentUserID string, limit, offset int) ([]domain.Community, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.GetCommunitiesByCountry(ctx, countryID, currentUserID, limit, offset)
}

func (s *communityService) GetCommunityByID(ctx context.Context, id string, currentUserID string) (*domain.Community, error) {
	return s.repo.GetCommunityByID(ctx, id, currentUserID)
}

func (s *communityService) ToggleJoinCommunity(ctx context.Context, communityID, userID string) (bool, int, error) {
	return s.repo.ToggleJoinCommunity(ctx, communityID, userID)
}

func (s *communityService) GetUserCommunities(ctx context.Context, username string) ([]domain.Community, error) {
	return s.repo.GetUserCommunities(ctx, username)
}
