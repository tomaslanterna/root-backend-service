package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

// visibilityDanceService: "crew-public" es pública, "crew-private" privada; "host-1" creó las dos.
type visibilityDanceService struct {
	ports.DanceService
	created    ports.CreateCrewRequest
	visibility map[string]bool
}

func (s *visibilityDanceService) CreatePermanentCrew(_ context.Context, _ string, req ports.CreateCrewRequest) (map[string]interface{}, error) {
	s.created = req
	return map[string]interface{}{"id": "crew-new", "isPublic": req.IsPublic}, nil
}

func (s *visibilityDanceService) JoinPublicCrew(_ context.Context, _ string, squadID string) (map[string]interface{}, error) {
	if squadID != "crew-public" {
		return nil, sql.ErrNoRows
	}
	return map[string]interface{}{"id": squadID}, nil
}

func (s *visibilityDanceService) SetCrewVisibility(_ context.Context, squadID, userID string, isPublic bool) error {
	if squadID != "crew-public" && squadID != "crew-private" {
		return sql.ErrNoRows
	}
	if userID != "host-1" {
		return ports.ErrNotCrewHost
	}
	s.visibility[squadID] = isPublic
	return nil
}

func visibilityRequest(method, target, userID, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return req.WithContext(ctx)
}

func TestCreateCrewReadsVisibility(t *testing.T) {
	svc := &visibilityDanceService{}
	rec := httptest.NewRecorder()
	h := &DanceHandler{danceService: svc}
	h.CreatePermanentCrew(rec, visibilityRequest(http.MethodPost, "/v1/crews", "user-1", `{"name":"Los de siempre","isPublic":true}`, nil))
	if rec.Code != http.StatusCreated || !svc.created.IsPublic {
		t.Fatalf("status %d, isPublic %v", rec.Code, svc.created.IsPublic)
	}

	svc = &visibilityDanceService{}
	rec = httptest.NewRecorder()
	(&DanceHandler{danceService: svc}).CreatePermanentCrew(rec, visibilityRequest(http.MethodPost, "/v1/crews", "user-1", `{"name":"Privada"}`, nil))
	if svc.created.IsPublic {
		t.Fatal("a crew without isPublic must be private")
	}
}

func TestJoinPublicCrew(t *testing.T) {
	h := &DanceHandler{danceService: &visibilityDanceService{}}

	rec := httptest.NewRecorder()
	h.JoinPublicCrew(rec, visibilityRequest(http.MethodPost, "/v1/crews/crew-public/join", "user-2", "", map[string]string{"id": "crew-public"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("public crew: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.JoinPublicCrew(rec, visibilityRequest(http.MethodPost, "/v1/crews/crew-private/join", "user-2", "", map[string]string{"id": "crew-private"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private crew must not be joinable without the invite: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.JoinPublicCrew(rec, visibilityRequest(http.MethodPost, "/v1/crews/crew-public/join", "", "", map[string]string{"id": "crew-public"}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without session: status %d", rec.Code)
	}
}

func TestUpdateCrewVisibility(t *testing.T) {
	svc := &visibilityDanceService{visibility: map[string]bool{}}
	h := &DanceHandler{danceService: svc}
	params := map[string]string{"id": "crew-private"}

	rec := httptest.NewRecorder()
	h.UpdateCrewVisibility(rec, visibilityRequest(http.MethodPatch, "/v1/crews/crew-private", "host-1", `{"isPublic":true}`, params))
	if rec.Code != http.StatusOK || !svc.visibility["crew-private"] {
		t.Fatalf("host: status %d, visibility %v", rec.Code, svc.visibility)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["isPublic"] != true {
		t.Fatalf("response %v", body)
	}

	rec = httptest.NewRecorder()
	h.UpdateCrewVisibility(rec, visibilityRequest(http.MethodPatch, "/v1/crews/crew-private", "user-2", `{"isPublic":false}`, params))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-host: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.UpdateCrewVisibility(rec, visibilityRequest(http.MethodPatch, "/v1/crews/crew-private", "host-1", `{}`, params))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing isPublic: status %d", rec.Code)
	}
}
