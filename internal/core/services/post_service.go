package services

import (
	"context"
	"sync"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type postService struct {
	postRepo ports.PostRepository
}

func NewPostService(repo ports.PostRepository) ports.PostService {
	return &postService{
		postRepo: repo,
	}
}

func (s *postService) GetFeeds(ctx context.Context, userID string, includeFeeds []string, pagination map[string]int) (map[string]ports.FeedData, error) {
	result := make(map[string]ports.FeedData)
	var mu sync.Mutex
	var wg sync.WaitGroup

	errChan := make(chan error, len(includeFeeds))

	for _, feed := range includeFeeds {
		feedType := feed // capture loop variable
		
		wg.Add(1)
		go func() {
			defer wg.Done()
			var posts []domain.Post
			var err error

			page := pagination[feedType+"_page"]
			limit := pagination[feedType+"_limit"]
			if page < 1 {
				page = 1
			}
			if limit < 1 {
				limit = 20
			}
			offset := (page - 1) * limit
			
			// Request limit + 1 to check if there is a next page
			fetchLimit := limit + 1

			switch feedType {
			case "global":
				posts, err = s.postRepo.GetGlobalPosts(ctx, fetchLimit, offset)
			case "featured":
				posts, err = s.postRepo.GetFeaturedPosts(ctx, fetchLimit, offset)
			case "following":
				if userID == "" {
					// Graceful fallback
					posts = []domain.Post{}
				} else {
					posts, err = s.postRepo.GetFollowingPosts(ctx, userID, fetchLimit, offset)
				}
			}

			if err != nil {
				errChan <- err
				return
			}

			if posts == nil {
				posts = []domain.Post{}
			}

			hasMore := false
			if len(posts) > limit {
				hasMore = true
				posts = posts[:limit] // trim the extra item used for checking
			}

			mu.Lock()
			result[feedType] = ports.FeedData{
				Data: posts,
				Pagination: map[string]interface{}{
					"page":     page,
					"limit":    limit,
					"has_more": hasMore,
					// total_items and total_pages are intentionally omitted for performance
				},
			}
			mu.Unlock()
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
