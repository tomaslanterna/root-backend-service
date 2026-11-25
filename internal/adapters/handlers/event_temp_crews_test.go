package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Detalle del evento con el resumen de crews temporales (`tempCrews`).

type eventTempCrewsService struct {
	ports.TempCrewService
	summary *domain.TempCrewsEventSummary
	err     error
	viewer  string
	started chan struct{}
	other   <-chan struct{}
	waited  bool
}

func (s *eventTempCrewsService) EventSummary(_ context.Context, _, viewerID string) (*domain.TempCrewsEventSummary, error) {
	s.viewer = viewerID
	if s.started != nil {
		close(s.started)
		select {
		case <-s.other:
			s.waited = true
		case <-time.After(2 * time.Second):
		}
	}
	return s.summary, s.err
}

type eventDetailStub struct {
	*eventServiceStub
	err     error
	started chan struct{}
	other   <-chan struct{}
	waited  bool
}

func (s *eventDetailStub) GetEventByID(ctx context.Context, id, user string) (*domain.Event, error) {
	if s.started != nil {
		close(s.started)
		select {
		case <-s.other:
			s.waited = true
		case <-time.After(2 * time.Second):
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.eventServiceStub.GetEventByID(ctx, id, user)
}

func summaryFixture(n int, viewerCrew *string) *domain.TempCrewsEventSummary {
	preview := make([]domain.TempCrewSummary, n)
	for i := range preview {
		preview[i] = domain.TempCrewSummary{ID: "crew-" + string(rune('a'+i)), Name: "Crew", Capacity: 5, MemberCount: 1, IsAdmitting: true,
			MembersPreview: []domain.TempCrewPerson{}, Event: domain.TempCrewEventRef{ID: "event-1"}}
	}
	return &domain.TempCrewsEventSummary{Total: 5, ViewerCrewID: viewerCrew, Preview: preview}
}

func getEventDetail(t *testing.T, handler *EventHandler, user string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/events/{id}", handler.GetEventByID)
	request := httptest.NewRequest(http.MethodGet, "/events/event-1", nil)
	if user != "" {
		request = request.WithContext(context.WithValue(request.Context(), UserIDKey, user))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response, body
}

func TestEventDetailIncludesTempCrewsSummary(t *testing.T) {
	crew := "crew-mine"
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	service := &eventTempCrewsService{summary: summaryFixture(3, &crew)}
	handler.SetTempCrewService(service)

	response, body := getEventDetail(t, handler, "user-1")
	if response.Code != http.StatusOK || service.viewer != "user-1" {
		t.Fatalf("status %d viewer=%q: %s", response.Code, service.viewer, response.Body.String())
	}
	var event map[string]interface{}
	_ = json.Unmarshal(response.Body.Bytes(), &event)
	if event["id"] != "event-1" {
		t.Fatalf("the event fields stay at the top level: %s", response.Body.String())
	}
	var crews struct {
		Total        int                      `json:"total"`
		ViewerCrewID *string                  `json:"viewerCrewId"`
		Preview      []map[string]interface{} `json:"preview"`
	}
	if err := json.Unmarshal(body["tempCrews"], &crews); err != nil {
		t.Fatalf("tempCrews missing: %s", response.Body.String())
	}
	if crews.Total != 5 || len(crews.Preview) != 3 || crews.ViewerCrewID == nil || *crews.ViewerCrewID != crew {
		t.Fatalf("unexpected summary: %+v", crews)
	}
	for _, key := range []string{"id", "name", "plan", "capacity", "memberCount", "isAdmitting", "host", "membersPreview", "event", "viewerRole", "viewerRequestStatus", "eventEnded"} {
		if _, ok := crews.Preview[0][key]; !ok {
			t.Errorf("preview item is not a TempCrewSummary, missing %q", key)
		}
	}
	if cache := response.Header().Get("Cache-Control"); cache != "private, no-store" {
		t.Errorf("a personalised response must not be cached publicly: %q", cache)
	}
}

func TestEventDetailWithoutSessionHasNoViewerData(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	service := &eventTempCrewsService{summary: summaryFixture(2, nil)}
	handler.SetTempCrewService(service)

	response, body := getEventDetail(t, handler, "")
	if response.Code != http.StatusOK || service.viewer != "" {
		t.Fatalf("status %d viewer=%q", response.Code, service.viewer)
	}
	var crews map[string]interface{}
	_ = json.Unmarshal(body["tempCrews"], &crews)
	if v, ok := crews["viewerCrewId"]; !ok || v != nil {
		t.Fatalf("viewerCrewId must be null without a session: %v", crews)
	}
	if cache := response.Header().Get("Cache-Control"); cache == "private, no-store" {
		t.Errorf("the anonymous response is the same for everyone: %q", cache)
	}
	if response.Header().Get("Vary") == "" {
		t.Error("the response varies by Authorization")
	}
}

func TestEventDetailWithoutCrewsHasEmptySummary(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetTempCrewService(&eventTempCrewsService{summary: &domain.TempCrewsEventSummary{Preview: []domain.TempCrewSummary{}}})
	_, body := getEventDetail(t, handler, "user-1")
	var crews map[string]json.RawMessage
	if err := json.Unmarshal(body["tempCrews"], &crews); err != nil {
		t.Fatal(err)
	}
	if string(crews["total"]) != "0" || string(crews["preview"]) != "[]" || string(crews["viewerCrewId"]) != "null" {
		t.Fatalf("empty event: total=%s preview=%s viewerCrewId=%s", crews["total"], crews["preview"], crews["viewerCrewId"])
	}
}

func TestEventDetailSurvivesATempCrewsFailure(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetTempCrewService(&eventTempCrewsService{err: errors.New("db down")})
	response, body := getEventDetail(t, handler, "user-1")
	if response.Code != http.StatusOK {
		t.Fatalf("the event is returned anyway: %d %s", response.Code, response.Body.String())
	}
	if _, present := body["tempCrews"]; present {
		t.Fatalf("tempCrews must be omitted when the query fails: %s", response.Body.String())
	}
	if string(body["id"]) != `"event-1"` {
		t.Fatalf("event fields still there: %s", response.Body.String())
	}
}

func TestEventDetailWithoutTempCrewServiceKeepsOldShape(t *testing.T) {
	response, body := getEventDetail(t, NewEventHandler(&eventServiceStub{}, nil, nil), "")
	if _, present := body["tempCrews"]; response.Code != http.StatusOK || present {
		t.Fatalf("unexpected: %d %s", response.Code, response.Body.String())
	}
}

func TestEventDetailMissingEventIsA404WithoutHanging(t *testing.T) {
	handler := NewEventHandler(&eventDetailStub{eventServiceStub: &eventServiceStub{}, err: sql.ErrNoRows}, nil, nil)
	handler.SetTempCrewService(&eventTempCrewsService{summary: summaryFixture(1, nil)})
	response, _ := getEventDetail(t, handler, "user-1")
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
}

func TestEventDetailFetchesEventAndCrewsConcurrently(t *testing.T) {
	eventStarted, crewsStarted := make(chan struct{}), make(chan struct{})
	events := &eventDetailStub{eventServiceStub: &eventServiceStub{}, started: eventStarted, other: crewsStarted}
	crews := &eventTempCrewsService{summary: summaryFixture(1, nil), started: crewsStarted, other: eventStarted}
	handler := NewEventHandler(events, nil, nil)
	handler.SetTempCrewService(crews)
	response, _ := getEventDetail(t, handler, "user-1")
	if response.Code != http.StatusOK || !events.waited || !crews.waited {
		t.Fatalf("each query must start while the other is still running: status=%d event=%v crews=%v", response.Code, events.waited, crews.waited)
	}
}

func TestEventListDoesNotIncludeTempCrews(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	handler.SetTempCrewService(&eventTempCrewsService{summary: summaryFixture(3, nil)})
	response := httptest.NewRecorder()
	handler.GetEvents(response, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	if containsKey(response.Body.Bytes(), "tempCrews") {
		t.Fatalf("the listing must not carry tempCrews: %s", response.Body.String())
	}
	featured := httptest.NewRecorder()
	handler.GetFeaturedEvents(featured, httptest.NewRequest(http.MethodGet, "/v1/events/featured", nil))
	if containsKey(featured.Body.Bytes(), "tempCrews") {
		t.Fatalf("neither do featured events: %s", featured.Body.String())
	}
}

func containsKey(raw []byte, key string) bool {
	var walk func(v interface{}) bool
	walk = func(v interface{}) bool {
		switch value := v.(type) {
		case map[string]interface{}:
			for k, child := range value {
				if k == key || walk(child) {
					return true
				}
			}
		case []interface{}:
			for _, child := range value {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	var decoded interface{}
	_ = json.Unmarshal(raw, &decoded)
	return walk(decoded)
}
