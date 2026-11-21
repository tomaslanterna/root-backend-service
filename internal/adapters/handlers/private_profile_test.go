package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type privacyUserService struct{ ports.UserService }

// "privada" es privada y solo la sigue "seguidor"; "publica" es pública.
func (privacyUserService) CanViewProfile(_ context.Context, viewerID, username string) (*domain.User, bool, error) {
	switch username {
	case "publica":
		return &domain.User{ID: "p1"}, true, nil
	case "privada":
		return &domain.User{ID: "p2", IsPrivate: true}, viewerID == "seguidor" || viewerID == "p2", nil
	}
	return nil, false, domain.ErrUserNotFound
}

func profileRequest(username, viewerID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+username+"/badges", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("username", username)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	if viewerID != "" {
		ctx = context.WithValue(ctx, UserIDKey, viewerID)
	}
	return req.WithContext(ctx)
}

func TestRequireProfileAccess(t *testing.T) {
	h := NewUserHandler(privacyUserService{}, nil)
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = ProfileUserID(r)
		w.WriteHeader(http.StatusOK)
	})
	cases := []struct {
		username, viewer string
		want             int
	}{
		{"publica", "", http.StatusOK},
		{"privada", "", http.StatusForbidden},
		{"privada", "otro", http.StatusForbidden},
		{"privada", "seguidor", http.StatusOK},
		{"privada", "p2", http.StatusOK},
		{"nadie", "", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		seen = ""
		h.RequireProfileAccess(next).ServeHTTP(rec, profileRequest(c.username, c.viewer))
		if rec.Code != c.want {
			t.Errorf("%s as %q: status %d, want %d", c.username, c.viewer, rec.Code, c.want)
		}
		if c.want == http.StatusOK && seen == "" {
			t.Errorf("%s: profile id missing in context", c.username)
		}
	}
}
