package search

import (
	"context"
	"root-backend-service/internal/core/ports"
)

type searchService struct {
	userRepo ports.UserRepository
}

func NewSearchService(userRepo ports.UserRepository) ports.SearchService {
	return &searchService{
		userRepo: userRepo,
	}
}

func (s *searchService) Search(ctx context.Context, query, searchType, country, currentUserID string) (interface{}, error) {
	switch searchType {
	case "USER":
		users, err := s.userRepo.SearchUsers(ctx, query, currentUserID)
		if err != nil {
			return nil, err
		}
		return users, nil
	case "EVENT":
		// Mock events for now
		return []interface{}{}, nil
	case "POST":
		// Mock posts for now
		return []interface{}{}, nil
	default:
		return []interface{}{}, nil
	}
}
