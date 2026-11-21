package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type followsUserService struct {
	ports.UserService
	gotUsername, gotViewer string
	gotLimit, gotOffset    int
	users                  []domain.FollowListUser
	total                  int
	err                    error
}

func (s *followsUserService) GetFollowers(_ context.Context, username, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	s.gotUsername, s.gotViewer, s.gotLimit, s.gotOffset = username, viewerID, limit, offset
	return s.users, s.total, s.err
}

func (s *followsUserService) GetFollowing(ctx context.Context, username, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	return s.GetFollowers(ctx, username, viewerID, limit, offset)
}

func followsRequest(path, username, viewerID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", username)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if viewerID != "" {
		ctx = context.WithValue(ctx, UserIDKey, viewerID)
	}
	return req.WithContext(ctx)
}

func TestListFollowersPaginatesAndResolvesAvatars(t *testing.T) {
	key := "avatars/u2/me.jpg"
	svc := &followsUserService{
		users: []domain.FollowListUser{{ID: "u2", Username: "ana", AvatarURL: &key, IsFollowing: true}},
		total: 3,
	}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, &avatarStorage{url: "https://signed/"}).
		ListFollowers(rec, followsRequest("/v1/users/mathi/followers?limit=1&offset=1", "mathi", "viewer-1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if svc.gotUsername != "mathi" || svc.gotViewer != "viewer-1" || svc.gotLimit != 1 || svc.gotOffset != 1 {
		t.Fatalf("service got %q %q %d %d", svc.gotUsername, svc.gotViewer, svc.gotLimit, svc.gotOffset)
	}
	var body struct {
		Data []domain.FollowListUser `json:"data"`
		Meta struct {
			Total   int  `json:"total"`
			HasMore bool `json:"hasMore"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || *body.Data[0].AvatarURL != "https://signed/avatars/u2/me.jpg" || !body.Data[0].IsFollowing {
		t.Fatalf("unexpected data %+v", body.Data)
	}
	if body.Meta.Total != 3 || !body.Meta.HasMore {
		t.Fatalf("unexpected meta %+v", body.Meta)
	}
}

func TestListFollowingWithoutSessionHasEmptyViewer(t *testing.T) {
	svc := &followsUserService{users: []domain.FollowListUser{}}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, nil).ListFollowing(rec, followsRequest("/v1/users/mathi/following", "mathi", ""))

	if rec.Code != http.StatusOK || svc.gotViewer != "" || svc.gotLimit != defaultRelatedPageSize {
		t.Fatalf("status %d viewer %q limit %d", rec.Code, svc.gotViewer, svc.gotLimit)
	}
}

func TestListFollowersUnknownUserIs404(t *testing.T) {
	svc := &followsUserService{err: domain.ErrUserNotFound}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, nil).ListFollowers(rec, followsRequest("/v1/users/nadie/followers", "nadie", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
