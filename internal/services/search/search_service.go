package search

import (
	"context"
	"strings"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type searchService struct {
	userRepo      ports.UserRepository
	eventRepo     ports.EventRepository
	communityRepo ports.CommunityRepository
}

func NewSearchService(userRepo ports.UserRepository, eventRepo ports.EventRepository, communityRepo ports.CommunityRepository) ports.SearchService {
	return &searchService{
		userRepo:      userRepo,
		eventRepo:     eventRepo,
		communityRepo: communityRepo,
	}
}

func (s *searchService) Search(ctx context.Context, query, searchType, country, currentUserID string) (interface{}, error) {
	cleanType := strings.ToUpper(strings.TrimSpace(searchType))
	switch cleanType {
	case "USER", "USERS":
		users, err := s.userRepo.SearchUsers(ctx, query, currentUserID)
		if err != nil {
			return nil, err
		}
		return users, nil
	case "EVENT", "EVENTS":
		if s.eventRepo != nil {
			events, err := s.eventRepo.SearchEvents(ctx, query)
			if err != nil {
				return nil, err
			}
			return events, nil
		}
		return []interface{}{}, nil
	case "COMMUNITY", "COMMUNITIES":
		if s.communityRepo != nil {
			filter := domain.CommunityFilter{
				Query: strings.TrimSpace(query),
				Limit: 30,
			}
			communities, _, err := s.communityRepo.GetCommunities(ctx, filter, currentUserID)
			if err != nil {
				return nil, err
			}
			return communities, nil
		}
		return []interface{}{}, nil
	case "POST", "POSTS":
		// Mock posts for now
		return []interface{}{}, nil
	default:
		return []interface{}{}, nil
	}
}

