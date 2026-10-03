package community

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
)

type communityService struct {
	repo ports.CommunityRepository
}

func NewCommunityService(repo ports.CommunityRepository) ports.CommunityService {
	return &communityService{repo: repo}
}

func (s *communityService) GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	filter.Country = strings.TrimSpace(filter.Country)
	filter.Category = strings.TrimSpace(filter.Category)
	filter.Department = strings.TrimSpace(filter.Department)
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Limit <= 0 {
		filter.Limit = 12
	}
	if filter.Limit > 50 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		return nil, 0, errors.New("offset must be non-negative")
	}
	return s.repo.GetCommunities(ctx, filter, currentUserID)
}

func (s *communityService) GetCommunity(ctx context.Context, identifier, currentUserID string) (*domain.Community, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, domain.ErrCommunityNotFound
	}
	return s.repo.GetCommunityByIDOrSlug(ctx, identifier, currentUserID)
}

func (s *communityService) JoinCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return s.repo.JoinCommunity(ctx, strings.TrimSpace(communityID), userID)
}

func (s *communityService) LeaveCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return s.repo.LeaveCommunity(ctx, strings.TrimSpace(communityID), userID)
}

func (s *communityService) GetUserCommunities(ctx context.Context, username, currentUserID string) ([]domain.Community, error) {
	return s.repo.GetUserCommunities(ctx, strings.TrimSpace(username), currentUserID)
}

func (s *communityService) GetCurrentUserCommunities(ctx context.Context, userID string) ([]domain.Community, error) {
	return s.repo.GetCurrentUserCommunities(ctx, userID)
}
