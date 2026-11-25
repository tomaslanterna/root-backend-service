package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Detalle del evento con los RRPPs oficiales (`rrpps`).

type eventRRPPServiceStub struct {
	ports.EventRRPPService
	rrpps   []domain.EventRRPP
	err     error
	eventID string
	calls   int
}

func (s *eventRRPPServiceStub) ListForEvent(_ context.Context, eventID string) ([]domain.EventRRPP, error) {
	s.calls++
	s.eventID = eventID
	return s.rrpps, s.err
}

func TestEventDetailIncludesRRPPsInOrderWithTheContractShape(t *testing.T) {
	service := &eventRRPPServiceStub{rrpps: []domain.EventRRPP{
		{UserID: "u-1", Name: "Valentina Sosa", Username: "valesosa", AvatarURL: strPtr("avatars/u-1/1.jpg"), Position: 1},
		{UserID: "u-2", Name: "Bruno Díaz", Username: "brunod", AvatarURL: nil, Position: 2},
	}}
	handler := NewEventHandler(&eventServiceStub{}, &avatarStorage{url: "https://cdn.test/"}, nil)
	handler.SetEventRRPPService(service)

	response, body := getEventDetail(t, handler, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if service.eventID != "event-1" {
		t.Fatalf("rrpps must be loaded for the event returned by the service, got %q", service.eventID)
	}
	want := `[{"userId":"u-1","name":"Valentina Sosa","username":"valesosa","avatarUrl":"https://cdn.test/avatars/u-1/1.jpg","position":1},` +
		`{"userId":"u-2","name":"Bruno Díaz","username":"brunod","avatarUrl":null,"position":2}]`
	if got := string(body["rrpps"]); got != want {
		t.Fatalf("rrpps JSON mismatch\n got: %s\nwant: %s", got, want)
	}
	if string(body["id"]) != `"event-1"` {
		t.Fatalf("event fields stay at the top level: %s", response.Body.String())
	}
}

func TestEventDetailWithoutRRPPsIsAnEmptyArray(t *testing.T) {
	cases := map[string]*eventRRPPServiceStub{
		"empty slice":      {rrpps: []domain.EventRRPP{}},
		"nil slice":        {rrpps: nil},
		"service disabled": nil,
	}
	for name, service := range cases {
		t.Run(name, func(t *testing.T) {
			handler := NewEventHandler(&eventServiceStub{}, nil, nil)
			if service != nil {
				handler.SetEventRRPPService(service)
			}
			response, body := getEventDetail(t, handler, "user-1")
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if got := string(body["rrpps"]); got != "[]" {
				t.Fatalf(`expected "rrpps": [], got %q in %s`, got, response.Body.String())
			}
		})
	}
}

func TestEventDetailRRPPsExposeOnlyPublicFields(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetEventRRPPService(&eventRRPPServiceStub{rrpps: []domain.EventRRPP{
		{UserID: "u-1", Name: "Valentina Sosa", Username: "valesosa", Position: 0},
	}})
	response, body := getEventDetail(t, handler, "")
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body["rrpps"], &items); err != nil || len(items) != 1 {
		t.Fatalf("rrpps: %v %s", err, response.Body.String())
	}
	allowed := map[string]bool{"userId": true, "name": true, "username": true, "avatarUrl": true, "position": true}
	for key := range items[0] {
		if !allowed[key] {
			t.Errorf("rrpp exposes a non-contract field %q", key)
		}
	}
	for key := range allowed {
		if _, ok := items[0][key]; !ok {
			t.Errorf("rrpp is missing %q", key)
		}
	}
	for _, private := range []string{"email", "phone", "phoneNumber", "documentId", "dob", "passwordHash", "isKycVerified", "kyc", "role"} {
		if containsKey(body["rrpps"], private) {
			t.Errorf("rrpps must not expose %q: %s", private, body["rrpps"])
		}
	}
}

func TestEventDetailFailsWhenRRPPsCannotBeLoaded(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetEventRRPPService(&eventRRPPServiceStub{err: errors.New("db down")})
	handler.SetTempCrewService(&eventTempCrewsService{summary: summaryFixture(1, nil)})
	response, _ := getEventDetail(t, handler, "user-1")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("a failed rrpps query must not be silent: %d %s", response.Code, response.Body.String())
	}
}

func TestEventDetailMissingEventDoesNotQueryRRPPs(t *testing.T) {
	service := &eventRRPPServiceStub{rrpps: []domain.EventRRPP{}}
	handler := NewEventHandler(&eventDetailStub{eventServiceStub: &eventServiceStub{}, err: sql.ErrNoRows}, nil, nil)
	handler.SetEventRRPPService(service)
	response, _ := getEventDetail(t, handler, "")
	if response.Code != http.StatusNotFound || service.calls != 0 {
		t.Fatalf("expected 404 without querying rrpps, got %d (calls=%d)", response.Code, service.calls)
	}
}

func TestEventListAndFeaturedDoNotIncludeRRPPs(t *testing.T) {
	service := &eventRRPPServiceStub{rrpps: []domain.EventRRPP{{UserID: "u-1", Name: "Vale", Username: "vale", Position: 1}}}
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetEventRRPPService(service)

	list := httptest.NewRecorder()
	handler.GetEvents(list, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if list.Code != http.StatusOK || containsKey(list.Body.Bytes(), "rrpps") {
		t.Fatalf("the listing must not carry rrpps: %d %s", list.Code, list.Body.String())
	}
	featured := httptest.NewRecorder()
	handler.GetFeaturedEvents(featured, httptest.NewRequest(http.MethodGet, "/v1/events/featured", nil))
	if containsKey(featured.Body.Bytes(), "rrpps") {
		t.Fatalf("neither do featured events: %s", featured.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("listing endpoints must not query rrpps (calls=%d)", service.calls)
	}
}
