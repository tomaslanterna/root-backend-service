package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type eventServiceStub struct {
	filter       domain.EventFilter
	currentUser  string
	rsvpUser     string
	rsvpStatus       string
	commentText      string
	createCommentErr error
	serviceCalls     int
	bannerEventID    string
	banner           string
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

func (s *eventServiceStub) GetFollowedGoingAttendees(context.Context, string, string, int, int) ([]domain.Attendee, int, error) {
	return []domain.Attendee{}, 0, nil
}

func (s *eventServiceStub) GetEventComments(context.Context, string, int, int) ([]domain.EventComment, int, error) {
	return []domain.EventComment{}, 0, nil
}

func (s *eventServiceStub) CreateEventComment(_ context.Context, eventID, userID, content string) (*domain.EventComment, error) {
	if s.createCommentErr != nil {
		return nil, s.createCommentErr
	}
	s.currentUser = userID
	s.commentText = content
	return &domain.EventComment{ID: "comment-1", TargetID: eventID, AuthorID: userID, Content: content}, nil
}

func (s *eventServiceStub) GetPendingSurveys(context.Context, string) ([]domain.Event, error) {
	return []domain.Event{}, nil
}

func (s *eventServiceStub) GetUserEvents(context.Context, string) ([]domain.Event, error) {
	return []domain.Event{}, nil
}

func (s *eventServiceStub) GetLiveEventStatus(context.Context, string, float64, float64) (*domain.Event, int, error) {
	return nil, 0, nil
}

func (s *eventServiceStub) BulkCreateFromCSV(ctx context.Context, r io.Reader) (int, error) {
	return 0, nil
}

func (s *eventServiceStub) UpdateEventBanner(_ context.Context, id string, banner string) error {
	s.serviceCalls++
	s.bannerEventID, s.banner = id, banner
	return nil
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
	handler := NewEventHandler(service, nil, nil)
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
	handler := NewEventHandler(service, nil, nil)
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
	handler := NewEventHandler(service, nil, nil)

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

func TestCreateEventCommentTrimsAndValidatesContent(t *testing.T) {
	service := &eventServiceStub{}
	handler := NewEventHandler(service, nil, nil)
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
	handler := NewEventHandler(&eventServiceStub{}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/events/event-1/attendees/followed", nil)
	request = requestWithRouteAndUser(request, "event-1", "")
	response := httptest.NewRecorder()

	handler.GetFollowedGoingAttendees(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestCreateEventCommentRejectsPolicyViolation(t *testing.T) {
	service := &eventServiceStub{createCommentErr: domain.ErrContentPolicyViolation}
	handler := NewEventHandler(service, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/events/event-1/comments", strings.NewReader(`{"content":"amenaza violenta"}`))
	request = requestWithRouteAndUser(request, "event-1", "user-1")
	response := httptest.NewRecorder()

	handler.CreateEventComment(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "políticas de violencia") {
		t.Fatalf("expected error message to mention violence policies, got: %s", response.Body.String())
	}
}

type bannerUserRepoStub struct {
	ports.UserRepository
	role string
}

func (u bannerUserRepoStub) GetUserByID(context.Context, string) (*domain.User, error) {
	return &domain.User{Role: u.role}, nil
}

type bannerS3Stub struct {
	s3.S3Service
	uploadedKey string
}

func (f *bannerS3Stub) UploadToS3(_ context.Context, _ io.Reader, key string, _ string) (string, error) {
	f.uploadedKey = key
	return "https://bucket/" + key, nil
}

func (f *bannerS3Stub) GenerateViewUrl(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://signed/" + key, nil
}

func bannerRequest(t *testing.T, eventID string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="flyer.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("jpeg"))
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPatch, "/v1/events/"+eventID+"/image", body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", eventID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, UserIDKey, "user-1")
	return request.WithContext(ctx)
}

func TestUpdateEventImageRejectsNonAdmins(t *testing.T) {
	service := &eventServiceStub{}
	storage := &bannerS3Stub{}
	handler := NewEventHandler(service, storage, bannerUserRepoStub{role: "USER"})
	response := httptest.NewRecorder()

	handler.UpdateEventImage(response, bannerRequest(t, "event-1"))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if storage.uploadedKey != "" || service.serviceCalls != 0 {
		t.Fatal("a non-admin must not upload images or touch the event")
	}
}

func TestUpdateEventImageStoresTheS3KeyForAdmins(t *testing.T) {
	service := &eventServiceStub{}
	storage := &bannerS3Stub{}
	handler := NewEventHandler(service, storage, bannerUserRepoStub{role: "ADMIN"})
	response := httptest.NewRecorder()

	handler.UpdateEventImage(response, bannerRequest(t, "event-1"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.bannerEventID != "event-1" || !strings.HasPrefix(service.banner, "events/event-1/") || service.banner != storage.uploadedKey {
		t.Fatalf("banner saved = %q for %q, uploaded %q", service.banner, service.bannerEventID, storage.uploadedKey)
	}
	var payload map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload["cinematicBannerUrl"] != "https://signed/"+storage.uploadedKey {
		t.Fatalf("unexpected response %v", payload)
	}
}
