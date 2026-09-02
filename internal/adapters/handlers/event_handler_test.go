package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type eventServiceStub struct {
	filter       domain.EventFilter
	currentUser  string
	rsvpUser     string
	rsvpStatus   string
	clearedRSVP  bool
	commentText  string
	serviceCalls int
}

func (s *eventServiceStub) GetFeaturedEvents(context.Context, string) ([]domain.Event, error) {
	return nil, nil
}

func (s *eventServiceStub) GetEvents(_ context.Context, filter domain.EventFilter, userID string) ([]domain.Event, int, error) {
	s.filter = filter
	s.currentUser = userID
	s.serviceCalls++
	return []domain.Event{{ID: "event-1"}}, 2, nil
}

func (s *eventServiceStub) GetEventByID(context.Context, string, string) (*domain.Event, error) {
	return &domain.Event{ID: "event-1"}, nil
}

func (s *eventServiceStub) RSVPEvent(_ context.Context, userID, _ string, status string) (int, int, string, error) {
	s.rsvpUser = userID
	s.rsvpStatus = status
	return 8, 3, status, nil
}

func (s *eventServiceStub) ClearEventRSVP(_ context.Context, userID, _ string) (int, int, error) {
	s.rsvpUser = userID
	s.clearedRSVP = true
	return 7, 3, nil
}

func (s *eventServiceStub) GetFollowedGoingAttendees(context.Context, string, string, int, int) ([]domain.Attendee, int, error) {
	return []domain.Attendee{}, 0, nil
}

func (s *eventServiceStub) GetEventComments(context.Context, string, int, int) ([]domain.EventComment, int, error) {
	return []domain.EventComment{}, 0, nil
}

func (s *eventServiceStub) CreateEventComment(_ context.Context, eventID, userID, content string) (*domain.EventComment, error) {
	s.currentUser = userID
	s.commentText = content
	return &domain.EventComment{ID: "comment-1", TargetID: eventID, AuthorID: userID, Content: content}, nil
}

func requestWithRouteAndUser(request *http.Request, eventID, userID string) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", eventID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return request.WithContext(ctx)
}

func TestGetEventsParsesCombinedFiltersAndPagination(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service)
	request := httptest.NewRequest(http.MethodGet,
		"/v1/events?genre=Electr%C3%B3nica&location=Montevideo&minPrice=100&maxPrice=500&isFree=false&startDate=2026-09-01T03:00:00Z&endDate=2026-09-30T02:59:59Z&limit=1&offset=0",
		nil,
	)
	request = request.WithContext(context.WithValue(request.Context(), UserIDKey, "user-1"))
	response := httptest.NewRecorder()

	handler.GetEvents(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.filter.Genre != "Electrónica" || service.filter.Location != "Montevideo" {
		t.Fatalf("unexpected filters: %+v", service.filter)
	}
	if service.filter.IsFree == nil || *service.filter.IsFree {
		t.Fatalf("expected paid filter, got %+v", service.filter.IsFree)
	}
	if service.filter.Limit != 1 || service.filter.Offset != 0 || service.currentUser != "user-1" {
		t.Fatalf("unexpected pagination/auth: %+v user=%s", service.filter, service.currentUser)
	}

	var body struct {
		Meta struct {
			Total   int  `json:"total"`
			HasMore bool `json:"hasMore"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Meta.Total != 2 || !body.Meta.HasMore {
		t.Fatalf("unexpected meta: %+v", body.Meta)
	}
}

func TestGetEventsRejectsInvalidRange(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service)
	request := httptest.NewRequest(http.MethodGet, "/v1/events?minPrice=500&maxPrice=100", nil)
	response := httptest.NewRecorder()

	handler.GetEvents(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if service.serviceCalls != 0 {
		t.Fatal("service must not be called for invalid filters")
	}
}

func TestRSVPUsesAuthenticatedUserAndRejectsArbitraryUserID(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service)

	invalid := httptest.NewRequest(http.MethodPost, "/v1/events/event-1/rsvp", strings.NewReader(`{"status":"going","userId":"other"}`))
	invalid = requestWithRouteAndUser(invalid, "event-1", "user-1")
	invalidResponse := httptest.NewRecorder()
	handler.RSVPEvent(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown userId to be rejected, got %d", invalidResponse.Code)
	}

	valid := httptest.NewRequest(http.MethodPost, "/v1/events/event-1/rsvp", strings.NewReader(`{"status":"not_going"}`))
	valid = requestWithRouteAndUser(valid, "event-1", "user-1")
	validResponse := httptest.NewRecorder()
	handler.RSVPEvent(validResponse, valid)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", validResponse.Code, validResponse.Body.String())
	}
	if service.rsvpUser != "user-1" || service.rsvpStatus != "not_going" {
		t.Fatalf("unexpected rsvp identity/status: %s %s", service.rsvpUser, service.rsvpStatus)
	}
}

func TestDeleteEventRSVPClearsAuthenticatedUsersSelection(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service)
	request := httptest.NewRequest(http.MethodDelete, "/v1/events/event-1/rsvp", nil)
	request = requestWithRouteAndUser(request, "event-1", "user-1")
	response := httptest.NewRecorder()

	handler.DeleteEventRSVP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !service.clearedRSVP || service.rsvpUser != "user-1" {
		t.Fatalf("authenticated RSVP was not cleared: %+v", service)
	}
	var body struct {
		GoingCount    int     `json:"goingCount"`
		NotGoingCount int     `json:"notGoingCount"`
		UserRSVP      *string `json:"userRsvp"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.GoingCount != 7 || body.NotGoingCount != 3 || body.UserRSVP != nil {
		t.Fatalf("unexpected RSVP removal response: %+v", body)
	}
}

func TestCreateEventCommentTrimsAndValidatesContent(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/events/event-1/comments", strings.NewReader(`{"content":"  Gran fecha  "}`))
	request = requestWithRouteAndUser(request, "event-1", "user-1")
	response := httptest.NewRecorder()

	handler.CreateEventComment(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.commentText != "Gran fecha" || service.currentUser != "user-1" {
		t.Fatalf("comment was not trimmed or authenticated: %q %q", service.commentText, service.currentUser)
	}
}

func TestFollowedAttendeesRequiresAuthentication(t *testing.T) {
	handler := NewEventHandler(&eventServiceStub{})
	request := httptest.NewRequest(http.MethodGet, "/v1/events/event-1/attendees/followed", nil)
	request = requestWithRouteAndUser(request, "event-1", "")
	response := httptest.NewRecorder()

	handler.GetFollowedGoingAttendees(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}
