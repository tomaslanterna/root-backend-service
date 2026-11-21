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

type inviteDanceService struct {
	ports.DanceService
	members    map[string]bool // userID -> is member of crew "crew-1"
	joinedWith string
}

func (s *inviteDanceService) GetCrewByID(_ context.Context, id string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": id, "name": "Los Bailarines", "inviteCode": "ABC123", "invite_code": "ABC123"}, nil
}
func (s *inviteDanceService) IsCrewMember(_ context.Context, _ string, userID string) (bool, error) {
	return s.members[userID], nil
}
func (s *inviteDanceService) GetAllCrews(context.Context) ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"id": "crew-1", "inviteCode": "ABC123", "invite_code": "ABC123"}}, nil
}
func (s *inviteDanceService) RegenerateCrewInviteCode(_ context.Context, _ string, userID string) (string, error) {
	if !s.members[userID] {
		return "", sql.ErrNoRows
	}
	return "NEWCODE123", nil
}
func (s *inviteDanceService) GetCrewInvitePreview(_ context.Context, code, _ string) (map[string]interface{}, error) {
	if code != "ABC123" {
		return nil, sql.ErrNoRows
	}
	return map[string]interface{}{"crewId": "crew-1", "name": "Los Bailarines", "memberCount": 3}, nil
}
func (s *inviteDanceService) JoinPermanentCrew(_ context.Context, _ string, code string) (map[string]interface{}, error) {
	s.joinedWith = code
	return map[string]interface{}{"id": "crew-1"}, nil
}

func inviteRequest(method, target, userID string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
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

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("invalid json: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestCrewInviteCodeIsOnlyVisibleToMembers(t *testing.T) {
	svc := &inviteDanceService{members: map[string]bool{"member": true}}
	h := NewDanceHandler(svc)

	for _, tc := range []struct {
		viewer   string
		wantCode bool
	}{{"member", true}, {"stranger", false}, {"", false}} {
		rec := httptest.NewRecorder()
		h.GetCrewByID(rec, inviteRequest(http.MethodGet, "/v1/crews/crew-1", tc.viewer, map[string]string{"id": "crew-1"}))
		_, hasCode := decodeMap(t, rec)["inviteCode"]
		if hasCode != tc.wantCode {
			t.Errorf("viewer %q: inviteCode present = %v, want %v", tc.viewer, hasCode, tc.wantCode)
		}
	}

	rec := httptest.NewRecorder()
	h.GetMyCrews(rec, httptest.NewRequest(http.MethodGet, "/v1/crews?scope=all", nil))
	if strings.Contains(rec.Body.String(), "ABC123") {
		t.Fatalf("the public crews list must not leak invite codes: %s", rec.Body.String())
	}
}

func TestJoinCrewRequiresAnInviteCode(t *testing.T) {
	svc := &inviteDanceService{}
	h := NewDanceHandler(svc)

	rec := httptest.NewRecorder()
	h.JoinPermanentCrew(rec, inviteRequest(http.MethodPost, "/v1/crews/x/join", "u1", map[string]string{"id": "crew-1"}))
	if rec.Code != http.StatusBadRequest || svc.joinedWith != "" {
		t.Fatalf("joining with only a crew id must be rejected: status %d, joined with %q", rec.Code, svc.joinedWith)
	}

	rec = httptest.NewRecorder()
	h.JoinPermanentCrew(rec, inviteRequest(http.MethodPost, "/v1/crews/join/ABC123", "u1", map[string]string{"inviteCode": "ABC123"}))
	if rec.Code != http.StatusOK || svc.joinedWith != "ABC123" {
		t.Fatalf("joining with the invite code failed: status %d, joined with %q", rec.Code, svc.joinedWith)
	}
}

func TestPreviewAndRegenerateInvite(t *testing.T) {
	svc := &inviteDanceService{members: map[string]bool{"member": true}}
	h := NewDanceHandler(svc)

	rec := httptest.NewRecorder()
	h.PreviewCrewInvite(rec, inviteRequest(http.MethodGet, "/v1/crews/invite/ABC123", "", map[string]string{"code": "ABC123"}))
	if rec.Code != http.StatusOK || decodeMap(t, rec)["name"] != "Los Bailarines" {
		t.Fatalf("preview failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.PreviewCrewInvite(rec, inviteRequest(http.MethodGet, "/v1/crews/invite/OLD", "", map[string]string{"code": "OLD"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a replaced code must 404, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.RegenerateInviteCode(rec, inviteRequest(http.MethodPost, "/v1/crews/crew-1/invite-code/regenerate", "stranger", map[string]string{"id": "crew-1"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a non-member must not regenerate the link, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.RegenerateInviteCode(rec, inviteRequest(http.MethodPost, "/v1/crews/crew-1/invite-code/regenerate", "member", map[string]string{"id": "crew-1"}))
	if rec.Code != http.StatusOK || decodeMap(t, rec)["inviteCode"] != "NEWCODE123" {
		t.Fatalf("a member must be able to regenerate: %d %s", rec.Code, rec.Body.String())
	}
}
