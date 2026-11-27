package community

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"unicode"
)

type communityService struct {
	repo ports.CommunityRepository
}

func NewCommunityService(repo ports.CommunityRepository) ports.CommunityService {
	return &communityService{repo: repo}
}

func (s *communityService) CreateCommunity(ctx context.Context, ownerID string, input domain.CreateCommunityInput) (*domain.Community, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.ToLower(strings.TrimSpace(input.Category))
	input.Zone = strings.TrimSpace(input.Zone)
	input.CountryID = strings.ToUpper(strings.TrimSpace(input.CountryID))
	input.Description = strings.TrimSpace(input.Description)

	if ownerID == "" || len([]rune(input.Name)) < 3 || len([]rune(input.Name)) > 80 ||
		len([]rune(input.Category)) < 2 || len([]rune(input.Category)) > 40 ||
		len([]rune(input.Zone)) > 80 || len([]rune(input.Description)) > 500 ||
		len(input.CountryID) != 2 {
		return nil, domain.ErrCommunityInvalid
	}
	for _, character := range input.CountryID {
		if !unicode.IsLetter(character) {
			return nil, domain.ErrCommunityInvalid
		}
	}
	return s.repo.CreateCommunity(ctx, ownerID, input)
}

func (s *communityService) GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	if filter.Scope != "" && filter.Scope != "mine" && filter.Scope != "explore" {
		return nil, 0, domain.ErrCommunityInvalid
	}
	if filter.Scope == "mine" && currentUserID == "" {
		return nil, 0, domain.ErrCommunityForbidden
	}
	filter.ViewerID = currentUserID
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
