package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type fakeTempCrewService struct {
	ports.TempCrewService
	err      error
	created  domain.CreateTempCrewInput
	userID   string
	message  string
	decided  string
	admitted *bool
	pending  bool
}

func (f *fakeTempCrewService) Create(_ context.Context, hostID, _ string, in domain.CreateTempCrewInput) (*domain.TempCrewSummary, error) {
	f.userID, f.created = hostID, in
	if f.err != nil {
		return nil, f.err
	}
	role := "host"
	count := 0
	return &domain.TempCrewSummary{
		ID: "crew-1", Name: in.Name, Plan: in.Plan, Capacity: in.Capacity, MemberCount: 1, IsAdmitting: true,
		Host:           domain.TempCrewPerson{UserID: hostID, Name: "Sofía", Username: "sofi"},
		MembersPreview: []domain.TempCrewPerson{{UserID: hostID, Name: "Sofía", Username: "sofi"}},
		Event:          domain.TempCrewEventRef{ID: "ev", Title: "Fiesta", Date: time.Date(2026, 12, 5, 23, 0, 0, 0, time.UTC), Location: "Montevideo"},
		ViewerRole:     &role, PendingRequestsCount: &count, EventEnded: false,
	}, nil
}

func (f *fakeTempCrewService) RequestJoin(_ context.Context, _, userID, message string) (domain.TempCrewJoinRequest, bool, error) {
	f.userID, f.message = userID, message
	return domain.TempCrewJoinRequest{ID: "req-1", CrewID: "crew-1", Status: "pending"}, !f.pending, f.err
}

func (f *fakeTempCrewService) Decide(_ context.Context, _, requestID, hostID string, accept bool) (domain.TempCrewJoinRequest, error) {
	f.userID = hostID
	f.decided = requestID
	if accept {
		f.decided += ":accept"
	} else {
		f.decided += ":reject"
	}
	return domain.TempCrewJoinRequest{ID: requestID, Status: "accepted"}, f.err
}

func (f *fakeTempCrewService) CancelMyRequest(context.Context, string, string) error { return f.err }

func (f *fakeTempCrewService) ListForEvent(_ context.Context, _, viewerID string, limit, offset int) (domain.TempCrewListPage, error) {
	f.userID = viewerID
	return domain.TempCrewListPage{Data: []domain.TempCrewSummary{}, Meta: domain.TempCrewListMeta{Limit: limit, Offset: offset}}, f.err
}

func (f *fakeTempCrewService) ListRequests(context.Context, string, string, string, int, int) ([]domain.TempCrewJoinRequest, error) {
	return []domain.TempCrewJoinRequest{}, f.err
}

func tempCrewRequest(h http.Handler, method, target, body, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if user != "" {
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, user))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tempCrewRouter(h *TempCrewHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/events/{id}/temp-crews", h.ListForEvent)
	r.Post("/events/{id}/temp-crews", h.Create)
	r.Post("/crews/{id}/join-requests", h.RequestJoin)
	r.Delete("/crews/{id}/join-requests/me", h.CancelMine)
	r.Get("/crews/{id}/join-requests", h.ListRequests)
	r.Post("/crews/{id}/join-requests/{requestId}/accept", h.Accept)
	r.Post("/crews/{id}/join-requests/{requestId}/reject", h.Reject)
	return r
}

func TestTempCrewCreateResponseShapeAndIdentity(t *testing.T) {
	svc := &fakeTempCrewService{}
	rec := tempCrewRequest(tempCrewRouter(NewTempCrewHandler(svc)), http.MethodPost, "/events/ev/temp-crews", `{"name":"Los del fondo","capacity":5,"plan":"Previa"}`, "user-jwt")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if svc.userID != "user-jwt" || svc.created.Capacity != 5 {
		t.Fatalf("host comes from the JWT: %+v", svc)
	}
	var body struct {
		Crew map[string]interface{} `json:"crew"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "plan", "capacity", "memberCount", "isAdmitting", "host", "membersPreview", "event", "viewerRole", "viewerRequestStatus", "pendingRequestsCount", "eventEnded"} {
		if _, ok := body.Crew[key]; !ok {
			t.Errorf("TempCrewSummary is missing %q: %s", key, rec.Body.String())
		}
	}
	event := body.Crew["event"].(map[string]interface{})
	for _, key := range []string{"id", "title", "date", "location", "cinematicBannerUrl"} {
		if _, ok := event[key]; !ok {
			t.Errorf("event ref is missing %q", key)
		}
	}
	if body.Crew["viewerRequestStatus"] != nil || body.Crew["viewerRole"] != "host" || body.Crew["eventEnded"] != false {
		t.Fatalf("unexpected values: %v", body.Crew)
	}
}

func TestTempCrewRejectsBadInput(t *testing.T) {
	router := tempCrewRouter(NewTempCrewHandler(&fakeTempCrewService{}))
	for name, tc := range map[string]struct{ method, target, body, user string }{
		"identity in the body": {http.MethodPost, "/events/ev/temp-crews", `{"name":"a","capacity":4,"plan":"p","userId":"someone-else"}`, "u"},
		"host in the body":     {http.MethodPost, "/events/ev/temp-crews", `{"name":"a","capacity":4,"plan":"p","hostId":"x"}`, "u"},
		"malformed json":       {http.MethodPost, "/events/ev/temp-crews", `{`, "u"},
		"empty create body":    {http.MethodPost, "/events/ev/temp-crews", ``, "u"},
		"user in join request": {http.MethodPost, "/crews/c/join-requests", `{"message":"hola","userId":"x"}`, "u"},
	} {
		rec := tempCrewRequest(router, tc.method, tc.target, tc.body, tc.user)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestTempCrewRequiresAuthentication(t *testing.T) {
	router := tempCrewRouter(NewTempCrewHandler(&fakeTempCrewService{}))
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/events/ev/temp-crews", `{"name":"a","capacity":4,"plan":"p"}`},
		{http.MethodPost, "/crews/c/join-requests", ``},
		{http.MethodDelete, "/crews/c/join-requests/me", ``},
		{http.MethodGet, "/crews/c/join-requests", ``},
		{http.MethodPost, "/crews/c/join-requests/r/accept", ``},
		{http.MethodPost, "/crews/c/join-requests/r/reject", ``},
	} {
		if rec := tempCrewRequest(router, tc.method, tc.target, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401, got %d", tc.method, tc.target, rec.Code)
		}
	}
}

func TestTempCrewErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&domain.ValidationError{Message: "bad"}, 400, "invalid_request"},
		{domain.ErrInvalidID, 400, "invalid_id"},
		{domain.ErrEventNotFound, 404, "event_not_found"},
		{domain.ErrTempCrewNotFound, 404, "crew_not_found"},
		{domain.ErrRequestNotFound, 404, "request_not_found"},
		{domain.ErrNotTempCrewHost, 403, "forbidden"},
		{domain.ErrEventEnded, 409, "event_ended"},
		{domain.ErrAlreadyInEventCrew, 409, "already_in_event_crew"},
		{domain.ErrAlreadyMember, 409, "already_member"},
		{domain.ErrNotAdmitting, 409, "not_admitting"},
		{domain.ErrCrewFull, 409, "crew_full"},
		{domain.ErrRequestNotPending, 409, "request_not_pending"},
		{domain.ErrTooManyRequests, 429, "too_many_requests"},
		{context.DeadlineExceeded, 500, "internal_error"},
	}
	for _, tc := range cases {
		router := tempCrewRouter(NewTempCrewHandler(&fakeTempCrewService{err: tc.err}))
		rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests/r/accept", ``, "host")
		var body struct{ Status, Message, Code string }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Code != tc.code || body.Status != "error" || body.Message == "" {
			t.Errorf("%v → %d %+v, want %d %s", tc.err, rec.Code, body, tc.status, tc.code)
		}
		if tc.status == 500 && strings.Contains(rec.Body.String(), "deadline") {
			t.Error("internal errors must not leak their text")
		}
	}
}

func TestTempCrewJoinRequestFlows(t *testing.T) {
	svc := &fakeTempCrewService{}
	router := tempCrewRouter(NewTempCrewHandler(svc))
	if rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests", ``, "u1"); rec.Code != http.StatusCreated {
		t.Fatalf("a request without body is valid: %d %s", rec.Code, rec.Body.String())
	}
	if rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests", `{"message":"Voy solo"}`, "u1"); rec.Code != http.StatusCreated || svc.message != "Voy solo" || svc.userID != "u1" {
		t.Fatalf("message passes through: %d %+v", rec.Code, svc)
	}
	svc.pending = true
	if rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests", ``, "u1"); rec.Code != http.StatusOK {
		t.Fatalf("an existing pending request is returned with 200: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodDelete, "/crews/c/join-requests/me", ``, "u1"); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel is 204: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests/req-9/accept", ``, "host"); rec.Code != http.StatusOK || svc.decided != "req-9:accept" || svc.userID != "host" {
		t.Fatalf("accept: %d %+v", rec.Code, svc)
	}
	if rec := tempCrewRequest(router, http.MethodPost, "/crews/c/join-requests/req-9/reject", ``, "host"); rec.Code != http.StatusOK || svc.decided != "req-9:reject" {
		t.Fatalf("reject: %d %+v", rec.Code, svc)
	}
}

func TestTempCrewEventListIsPublicButPersonalizedWithJWT(t *testing.T) {
	svc := &fakeTempCrewService{}
	router := tempCrewRouter(NewTempCrewHandler(svc))
	rec := tempCrewRequest(router, http.MethodGet, "/events/ev/temp-crews?limit=5&offset=10", ``, "")
	if rec.Code != http.StatusOK || svc.userID != "" {
		t.Fatalf("anonymous listing: %d viewer=%q", rec.Code, svc.userID)
	}
	var page struct {
		Data []interface{}          `json:"data"`
		Meta map[string]interface{} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total", "limit", "offset", "hasMore", "viewerCrewId"} {
		if _, ok := page.Meta[key]; !ok {
			t.Errorf("meta is missing %q: %s", key, rec.Body.String())
		}
	}
	if page.Meta["limit"] != float64(5) || page.Meta["offset"] != float64(10) || page.Data == nil {
		t.Fatalf("limit and offset are forwarded and data is never null: %s", rec.Body.String())
	}
	tempCrewRequest(router, http.MethodGet, "/events/ev/temp-crews", ``, "viewer-1")
	if svc.userID != "viewer-1" {
		t.Fatalf("viewer comes from the JWT: %q", svc.userID)
	}
}
