package search

import (
	"context"
	"root-backend-service/internal/core/ports"
)

type searchService struct {
	userRepo  ports.UserRepository
	eventRepo ports.EventRepository
}

func NewSearchService(userRepo ports.UserRepository, eventRepo ports.EventRepository) ports.SearchService {
	return &searchService{
		userRepo:  userRepo,
		eventRepo: eventRepo,
	}
}

func (s *searchService) Search(ctx context.Context, query, searchType, country, currentUserID string) (interface{}, error) {
	switch searchType {
	case "usuarios":
		users, err := s.userRepo.SearchUsers(ctx, query, currentUserID)
		if err != nil {
			return nil, err
		}
		return users, nil
	case "eventos":
		if s.eventRepo != nil {
			events, err := s.eventRepo.SearchEvents(ctx, query)
			if err != nil {
				return nil, err
			}
			return events, nil
		}
		return []interface{}{}, nil
	case "posteos":
		// Mock posts for now
		return []interface{}{}, nil
	default:
		return []interface{}{}, nil
	}
}
