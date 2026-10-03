package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"testing"
	"time"
)

type postRepositoryForCommunityStub struct {
	created *domain.Post
}

func (s *postRepositoryForCommunityStub) GetGlobalPosts(context.Context, int, int) ([]domain.Post, error) {
	return nil, nil
}
func (s *postRepositoryForCommunityStub) GetFeaturedPosts(context.Context, int, int) ([]domain.Post, error) {
	return nil, nil
}
func (s *postRepositoryForCommunityStub) GetFollowingPosts(context.Context, string, int, int) ([]domain.Post, error) {
	return nil, nil
}
func (s *postRepositoryForCommunityStub) GetPostByID(context.Context, string) (*domain.Post, error) {
	return s.created, nil
}
func (s *postRepositoryForCommunityStub) GetCommunityPosts(context.Context, string, int, int) ([]domain.Post, int, error) {
	return []domain.Post{}, 0, nil
}
func (s *postRepositoryForCommunityStub) CreatePost(_ context.Context, post *domain.Post) error {
	post.ID = "post-1"
	post.Timestamp = time.Now()
	copy := *post
	s.created = &copy
	return nil
}
func (s *postRepositoryForCommunityStub) GetPostComments(context.Context, string, int, int) ([]domain.EventComment, int, error) {
	return nil, 0, nil
}
func (s *postRepositoryForCommunityStub) CreatePostComment(context.Context, string, string, string) (*domain.EventComment, error) {
	return nil, nil
}

type communityRepositoryForPostStub struct {
	canPublish bool
}

func (s *communityRepositoryForPostStub) InitSchema(context.Context) error { return nil }
func (s *communityRepositoryForPostStub) GetCommunities(context.Context, domain.CommunityFilter, string) ([]domain.Community, int, error) {
	return nil, 0, nil
}
func (s *communityRepositoryForPostStub) GetCommunityByIDOrSlug(_ context.Context, _ string, _ string) (*domain.Community, error) {
	return &domain.Community{ID: "community-1", CanPublish: s.canPublish}, nil
}
func (s *communityRepositoryForPostStub) JoinCommunity(context.Context, string, string) (int, error) {
	return 0, nil
}
func (s *communityRepositoryForPostStub) LeaveCommunity(context.Context, string, string) (int, error) {
	return 0, nil
}
func (s *communityRepositoryForPostStub) GetUserCommunities(context.Context, string, string) ([]domain.Community, error) {
	return nil, nil
}
func (s *communityRepositoryForPostStub) GetCurrentUserCommunities(context.Context, string) ([]domain.Community, error) {
	return nil, nil
}
func (s *communityRepositoryForPostStub) CanPublish(context.Context, string, string) (bool, bool, error) {
	return true, s.canPublish, nil
}

func TestCreateCommunityAnnouncementRejectsRegularMembers(t *testing.T) {
	service := NewPostService(&postRepositoryForCommunityStub{}, &communityRepositoryForPostStub{canPublish: false})
	_, err := service.CreateCommunityAnnouncement(context.Background(), "community-1", "user-1", &domain.Post{Content: "Aviso"})
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected forbidden error, got %v", err)
	}
}

func TestCreateCommunityAnnouncementPersistsOneAuthorizedPost(t *testing.T) {
	postRepository := &postRepositoryForCommunityStub{}
	service := NewPostService(postRepository, &communityRepositoryForPostStub{canPublish: true})

	created, err := service.CreateCommunityAnnouncement(context.Background(), "electronica", "rrpp-1", &domain.Post{Content: "  Nueva fecha confirmada  "})
	if err != nil {
		t.Fatalf("creating announcement: %v", err)
	}
	if created.ID != "post-1" || created.Content != "Nueva fecha confirmada" {
		t.Fatalf("unexpected created announcement: %+v", created)
	}
	if created.CommunityID == nil || *created.CommunityID != "community-1" || created.AuthorID != "rrpp-1" {
		t.Fatalf("community or author was not enforced: %+v", created)
	}
}

func TestGenericCommunityPostCannotBypassPublishingPermission(t *testing.T) {
	communityID := "community-1"
	service := NewPostService(&postRepositoryForCommunityStub{}, &communityRepositoryForPostStub{canPublish: false})
	err := service.CreatePost(context.Background(), &domain.Post{AuthorID: "member-1", CommunityID: &communityID, Content: "Bypass"})
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected forbidden error, got %v", err)
	}
}
