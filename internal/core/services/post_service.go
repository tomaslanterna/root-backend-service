package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"sync"
)

type postService struct {
	postRepo      ports.PostRepository
	communityRepo ports.CommunityRepository
}

func NewPostService(repo ports.PostRepository, communityRepo ports.CommunityRepository) ports.PostService {
	return &postService{
		postRepo:      repo,
		communityRepo: communityRepo,
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

func (s *postService) GetPostByID(ctx context.Context, id string, currentUserID string) (*domain.Post, error) {
	return s.postRepo.GetPostByID(ctx, id)
}

func (s *postService) GetPostComments(ctx context.Context, postID string, limit, offset int) ([]domain.EventComment, int, error) {
	return s.postRepo.GetPostComments(ctx, postID, limit, offset)
}

func (s *postService) CreatePostComment(ctx context.Context, postID, authorID, content string) (*domain.EventComment, error) {
	return s.postRepo.CreatePostComment(ctx, postID, authorID, content)
}

func (s *postService) CreatePost(ctx context.Context, post *domain.Post) error {
	if post.CommunityID != nil && strings.TrimSpace(*post.CommunityID) != "" {
		community, err := s.communityRepo.GetCommunityByIDOrSlug(ctx, *post.CommunityID, post.AuthorID)
		if err != nil {
			return err
		}
		if !community.CanPublish {
			return domain.ErrCommunityForbidden
		}
		post.CommunityID = &community.ID
	}
	post.Content = strings.TrimSpace(post.Content)
	if post.Title != nil {
		title := strings.TrimSpace(*post.Title)
		post.Title = &title
	}
	if (post.Title == nil || *post.Title == "") && post.Content == "" {
		return errors.New("post must have a title or content")
	}
	return s.postRepo.CreatePost(ctx, post)
}

func (s *postService) GetCommunityAnnouncements(ctx context.Context, communityID string, limit, offset int) ([]domain.Post, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must be non-negative")
	}
	community, err := s.communityRepo.GetCommunityByIDOrSlug(ctx, strings.TrimSpace(communityID), "")
	if err != nil {
		return nil, 0, err
	}
	return s.postRepo.GetCommunityPosts(ctx, community.ID, limit, offset)
}

func (s *postService) CreateCommunityAnnouncement(ctx context.Context, communityID, authorID string, post *domain.Post) (*domain.Post, error) {
	community, err := s.communityRepo.GetCommunityByIDOrSlug(ctx, strings.TrimSpace(communityID), authorID)
	if err != nil {
		return nil, err
	}
	if !community.CanPublish {
		return nil, domain.ErrCommunityForbidden
	}

	post.AuthorID = authorID
	post.CommunityID = &community.ID
	post.Content = strings.TrimSpace(post.Content)
	if post.Title != nil {
		title := strings.TrimSpace(*post.Title)
		post.Title = &title
		if len([]rune(title)) > 120 {
			return nil, errors.New("announcement title cannot exceed 120 characters")
		}
	}
	if (post.Title == nil || *post.Title == "") && post.Content == "" {
		return nil, errors.New("announcement must have a title or content")
	}
	if len([]rune(post.Content)) > 2000 {
		return nil, errors.New("announcement content cannot exceed 2000 characters")
	}
	if err := s.postRepo.CreatePost(ctx, post); err != nil {
		return nil, err
	}
	created, err := s.postRepo.GetPostByID(ctx, post.ID)
	if err != nil {
		return nil, err
	}
	return created, nil
}
