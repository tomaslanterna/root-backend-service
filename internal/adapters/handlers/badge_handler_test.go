package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
)

type badgeServiceStub struct {
	viewer, username, eventID string
	limit, offset, calls      int
	err                       error
}

func (s *badgeServiceStub) List(_ context.Context, username, viewer string, limit, offset int) ([]domain.EventBadge, int, error) {
	s.viewer, s.username, s.limit, s.offset = viewer, username, limit, offset
	s.calls++
	return []domain.EventBadge{{EventID: "one"}}, 20, s.err
}
func (s *badgeServiceStub) Get(_ context.Context, viewer, eventID string) (*domain.EventBadge, error) {
	s.viewer, s.eventID = viewer, eventID
	s.calls++
	return &domain.EventBadge{EventID: eventID}, s.err
}

func TestBadgeListPaginationAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		query                 string
		status, limit, offset int
	}{
		{"", 200, 12, 0}, {"?limit=1&offset=3&userId=someone-else", 200, 1, 3},
		{"?limit=0", 400, 0, 0}, {"?limit=51", 400, 0, 0},
		{"?limit=abc", 400, 0, 0}, {"?offset=-1", 400, 0, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			service := &badgeServiceStub{}
			req := httptest.NewRequest("GET", "/"+tc.query, nil)
			ctx := chi.NewRouteContext()
			ctx.URLParams.Add("username", "owner")
			req = req.WithContext(context.WithValue(context.WithValue(req.Context(), chi.RouteCtxKey, ctx), UserIDKey, "jwt-viewer"))
			w := httptest.NewRecorder()
			NewBadgeHandler(service).List(w, req)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("authenticated data may be cached")
			}
			if tc.status == 400 {
				if service.calls != 0 {
					t.Fatal("invalid query reached service")
				}
				return
			}
			if service.viewer != "jwt-viewer" || service.username != "owner" || service.limit != tc.limit || service.offset != tc.offset {
				t.Fatal("wrong identity or pagination")
			}
			var body struct {
				Meta struct {
					Total   int
					HasMore bool
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Meta.Total != 20 || !body.Meta.HasMore {
				t.Fatal("total is not full collection count")
			}
		})
	}
}

func TestBadgeDetailAuthenticationAndErrors(t *testing.T) {
	const eventID = "d7c7d173-7a9e-4f96-a1e2-91d59a4f2983"
	for _, tc := range []struct {
		user, event string
		err         error
		status      int
	}{
		{"", eventID, nil, 401}, {"owner", "invalid", nil, 400},
		{"owner", eventID, sql.ErrNoRows, 404}, {"owner", eventID, errors.New("database offline"), 500},
		{"owner", eventID, nil, 200},
	} {
		service := &badgeServiceStub{err: tc.err}
		req := requestWithRouteAndUser(httptest.NewRequest("GET", "/?userId=someone-else", nil), tc.event, tc.user)
		w := httptest.NewRecorder()
		NewBadgeHandler(service).Get(w, req)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
		if service.calls > 0 && service.viewer != tc.user {
			t.Fatal("did not bind own badge to JWT")
		}
	}
}

func TestLivePresenceRejectsInvalidCoordinates(t *testing.T) {
	for _, query := range []string{"?lat=NaN&lng=0", "?lat=0&lng=Inf", "?lat=91&lng=0", "?lat=0&lng=-181", "?lat=1", "?lat=abc&lng=0"} {
		// A nil service makes an accidental call past validation fail the test.
		req := requestWithRouteAndUser(httptest.NewRequest("GET", "/"+query, nil), "", "owner")
		w := httptest.NewRecorder()
		NewEventHandler(nil).GetLiveEventStatus(w, req)
		if w.Code != 400 {
			t.Fatalf("%s: got %d", query, w.Code)
		}
	}
}
