package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Crews temporales sobre los endpoints compartidos de /v1/crews: "temp" es vigente, "old" con el evento terminado, "perm" permanente.
type tempVisibilityDance struct{ ports.DanceService }

func (tempVisibilityDance) GetCrewByID(_ context.Context, id string) (map[string]interface{}, error) {
	switch id {
	case "temp", "old":
		return map[string]interface{}{"id": id, "type": "temporary", "inviteCode": "ABC12345", "invite_code": "ABC12345", "events": []map[string]interface{}{}}, nil
	case "perm":
		return map[string]interface{}{"id": id, "type": "permanent", "inviteCode": "ABC12345", "events": []map[string]interface{}{}}, nil
	}
	return nil, sql.ErrNoRows
}

func (tempVisibilityDance) IsCrewMember(_ context.Context, id, userID string) (bool, error) {
	return userID == "member" && id != "perm", nil
}

func (tempVisibilityDance) GetCrewLeaderboards(context.Context, string, *string) (map[string]interface{}, error) {
	return map[string]interface{}{"ranking": []string{}}, nil
}

type tempVisibilityCrews struct{ ports.TempCrewService }

func (tempVisibilityCrews) Detail(_ context.Context, id, viewerID string) (*domain.TempCrewDetail, error) {
	if id == "perm" {
		return nil, nil
	}
	detail := &domain.TempCrewDetail{HostID: "host", Plan: "Previa", Capacity: 5, IsAdmitting: id != "old", EventEnded: id == "old"}
	if viewerID == "member" || viewerID == "host" {
		role := "member"
		detail.ViewerRole = &role
	}
	return detail, nil
}

func (tempVisibilityCrews) SetAdmitting(_ context.Context, id, userID string, admitting bool) (bool, error) {
	if userID != "host" {
		return false, domain.ErrNotTempCrewHost
	}
	return admitting, nil
}

func tempVisibilityRouter() http.Handler {
	h := NewDanceHandler(tempVisibilityDance{})
	h.SetTempCrewService(tempVisibilityCrews{})
	r := chi.NewRouter()
	r.Get("/crews/{id}", h.GetCrewByID)
	r.Get("/crews/{id}/rankings", h.GetCrewLeaderboards)
	r.Patch("/crews/{id}", h.UpdateCrewVisibility)
	return r
}

func TestTempCrewDetailVisibility(t *testing.T) {
	router := tempVisibilityRouter()
	get := func(target, user string) (int, map[string]interface{}) {
		rec := tempCrewRequest(router, http.MethodGet, target, ``, user)
		var body map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	if code, _ := get("/crews/temp", ""); code != http.StatusUnauthorized {
		t.Errorf("a temporary crew needs a session: %d", code)
	}
	code, body := get("/crews/temp", "stranger")
	if code != http.StatusOK || body["tempCrew"] == nil || body["type"] != "temporary" {
		t.Fatalf("any signed-in user sees an open temporary crew: %d %v", code, body)
	}
	if _, leaked := body["inviteCode"]; leaked {
		t.Error("temporary crews never expose an invite code, not even to members")
	}
	if _, leaked := get2(t, router, "/crews/temp", "member")["inviteCode"]; leaked {
		t.Error("members don't get an invite code either: entry is always by approval")
	}
	// Terminado el evento la crew sigue en la app para todos; solo deja de admitir gente.
	for _, user := range []string{"stranger", "member"} {
		code, body := get("/crews/old", user)
		extras, _ := body["tempCrew"].(map[string]interface{})
		if code != http.StatusOK || extras == nil || extras["eventEnded"] != true || extras["isAdmitting"] != false {
			t.Errorf("a crew whose event ended stays visible (%s): %d %v", user, code, body)
		}
	}
	// Las permanentes no cambian.
	code, body = get("/crews/perm", "")
	if code != http.StatusOK || body["tempCrew"] != nil {
		t.Errorf("permanent crews are untouched: %d %v", code, body)
	}
	if code, _ := get("/crews/missing", "member"); code != http.StatusNotFound {
		t.Errorf("unknown crew: %d", code)
	}
}

func get2(t *testing.T, router http.Handler, target, user string) map[string]interface{} {
	t.Helper()
	rec := tempCrewRequest(router, http.MethodGet, target, ``, user)
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTempCrewRankingsAreMembersOnly(t *testing.T) {
	router := tempVisibilityRouter()
	if rec := tempCrewRequest(router, http.MethodGet, "/crews/temp/rankings", ``, "stranger"); rec.Code != http.StatusForbidden {
		t.Errorf("non-members cannot read the ranking: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodGet, "/crews/temp/rankings", ``, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous cannot read the ranking: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodGet, "/crews/temp/rankings", ``, "member"); rec.Code != http.StatusOK {
		t.Errorf("members can: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodGet, "/crews/perm/rankings", ``, ""); rec.Code != http.StatusOK {
		t.Errorf("permanent crews keep their current behaviour: %d", rec.Code)
	}
}

func TestTempCrewPatchAdmitting(t *testing.T) {
	router := tempVisibilityRouter()
	rec := tempCrewRequest(router, http.MethodPatch, "/crews/temp", `{"isAdmitting":false}`, "host")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"isAdmitting":false}` {
		t.Fatalf("host closes requests: %d %s", rec.Code, rec.Body.String())
	}
	if rec := tempCrewRequest(router, http.MethodPatch, "/crews/temp", `{"isAdmitting":true}`, "someone"); rec.Code != http.StatusForbidden {
		t.Errorf("only the host: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodPatch, "/crews/temp", `{}`, "host"); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty patch is a 400: %d", rec.Code)
	}
	if rec := tempCrewRequest(router, http.MethodPatch, "/crews/temp", `{"isAdmitting":true}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}
}
