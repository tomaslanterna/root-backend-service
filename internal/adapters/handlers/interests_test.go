package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type interestsUserService struct {
	ports.UserService
	saved []string
}

func (s *interestsUserService) GetInterests(context.Context, string) ([]string, bool, error) {
	return []string{"House"}, true, nil
}

func (s *interestsUserService) SetInterests(_ context.Context, _ string, genres []string) ([]string, error) {
	clean, err := domain.NormalizeInterests(genres)
	if err != nil {
		return nil, err
	}
	s.saved = clean
	return clean, nil
}

func interestsRequest(method, body, userID string) *http.Request {
	req := httptest.NewRequest(method, "/v1/users/me/interests", strings.NewReader(body))
	if userID != "" {
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
	}
	return req
}

func TestGetMyInterests(t *testing.T) {
	rec := httptest.NewRecorder()
	NewUserHandler(&interestsUserService{}, nil).GetMyInterests(rec, interestsRequest(http.MethodGet, "", "u1"))
	var body struct {
		Genres   []string `json:"genres"`
		Answered bool     `json:"answered"`
		Options  []string `json:"options"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || !body.Answered || len(body.Genres) != 1 || len(body.Options) != len(domain.MusicGenres) {
		t.Fatalf("status %d body %+v", rec.Code, body)
	}

	rec = httptest.NewRecorder()
	NewUserHandler(&interestsUserService{}, nil).GetMyInterests(rec, interestsRequest(http.MethodGet, "", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without session: %d", rec.Code)
	}
}

func TestUpdateMyInterests(t *testing.T) {
	svc := &interestsUserService{}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, nil).UpdateMyInterests(rec, interestsRequest(http.MethodPut, `{"genres":["techno","Afro House"]}`, "u1"))
	if rec.Code != http.StatusOK || len(svc.saved) != 2 || svc.saved[0] != "Techno" {
		t.Fatalf("status %d saved %v", rec.Code, svc.saved)
	}

	rec = httptest.NewRecorder()
	NewUserHandler(svc, nil).UpdateMyInterests(rec, interestsRequest(http.MethodPut, `{"genres":["Cumbia"]}`, "u1"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown genre: %d", rec.Code)
	}

	// "Ahora no": lista vacía, igual queda respondido.
	rec = httptest.NewRecorder()
	NewUserHandler(svc, nil).UpdateMyInterests(rec, interestsRequest(http.MethodPut, `{"genres":[]}`, "u1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list: %d", rec.Code)
	}
}

type featuredEventService struct{ ports.EventService }

func (featuredEventService) GetFeaturedEvents(context.Context, string) ([]domain.Event, error) {
	return []domain.Event{{ID: "cumbia", Title: "Fiesta de cumbia"}, {ID: "house", Title: "Noche house"}}, nil
}

type interestsUserRepo struct{ ports.UserRepository }

func (interestsUserRepo) GetInterests(context.Context, string) ([]string, bool, error) {
	return []string{"House"}, true, nil
}

func TestFeaturedEventsFollowInterests(t *testing.T) {
	h := NewEventHandler(featuredEventService{}, nil, interestsUserRepo{})
	ids := func(userID string) []string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/events/featured", nil)
		if userID != "" {
			req = req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
		}
		h.GetFeaturedEvents(rec, req)
		var body struct {
			Data []domain.Event `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := []string{}
		for _, e := range body.Data {
			out = append(out, e.ID)
		}
		return out
	}
	if got := ids("u1"); len(got) != 2 || got[0] != "house" {
		t.Fatalf("with session the house event goes first, got %v", got)
	}
	if got := ids(""); len(got) != 2 || got[0] != "cumbia" {
		t.Fatalf("without session the order stays, got %v", got)
	}
}
