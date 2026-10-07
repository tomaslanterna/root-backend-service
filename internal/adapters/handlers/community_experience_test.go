package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"testing"
	"time"
)

type experienceServiceStub struct {
	ports.CommunityService
	userID string
	err    error
	calls  int
}

func TestAnnouncementWatermarkPreservesPrecisionAndIgnoresPinOrder(t *testing.T) {
	base := time.Now().UTC()
	posts := []domain.Post{{ID: "z", Timestamp: base, IsPinned: true}, {ID: "a", Timestamp: base.Add(time.Microsecond)}}
	if id := latestCommunityAnnouncementID(posts); id != "a" {
		t.Fatalf("microsecond timestamp ignored: %s", id)
	}
	posts = append(posts, domain.Post{ID: "b", Timestamp: base.Add(time.Microsecond)})
	if id := latestCommunityAnnouncementID(posts); id != "b" {
		t.Fatalf("tie-breaker ignored: %s", id)
	}
	if id := latestCommunityAnnouncementID(nil); id != "" {
		t.Fatal("empty page has watermark")
	}
}

func (s *experienceServiceStub) SetMuted(_ context.Context, _, userID string, _ bool) error {
	s.calls++
	s.userID = userID
	return s.err
}
func (s *experienceServiceStub) CreateReport(_ context.Context, _, userID string, _ domain.CommunityReportInput) (string, error) {
	s.calls++
	s.userID = userID
	return "report", s.err
}
func TestCommunityExperienceHTTPAuthenticationAndStrictBody(t *testing.T) {
	for _, tc := range []struct {
		body, user string
		status     int
	}{
		{`{"muted":true}`, "", 401},
		{`{"muted":true,"userId":"someone-else"}`, "jwt-user", 400},
		{`{}`, "jwt-user", 400},
		{`{"muted":null}`, "jwt-user", 400},
		{`{"muted":"true"}`, "jwt-user", 400},
		{`{"muted":true} {}`, "jwt-user", 400},
		{`{"muted":true}`, "jwt-user", 200},
	} {
		service := &experienceServiceStub{}
		handler := NewCommunityHandler(service)
		response := httptest.NewRecorder()
		request := communityRequestWithRouteAndUser(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body)), "slug", tc.user)
		handler.SetMuted(response, request)
		if response.Code != tc.status {
			t.Fatalf("body %s user %s: %d %s", tc.body, tc.user, response.Code, response.Body.String())
		}
		if tc.status == 200 && service.userID != "jwt-user" {
			t.Fatal("identity did not come from authentication")
		}
		if tc.status != 200 && service.calls != 0 {
			t.Fatal("invalid request reached service")
		}
	}
}
func TestCommunityExperienceHTTPDomainErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{domain.ErrCommunityForbidden, 403}, {domain.ErrCommunityNotFound, 404}, {domain.ErrCommunityTargetNotFound, 404}, {domain.ErrCommunityInvalid, 400}} {
		service := &experienceServiceStub{err: tc.err}
		response := httptest.NewRecorder()
		request := communityRequestWithRouteAndUser(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"muted":false}`)), "slug", "jwt-user")
		NewCommunityHandler(service).SetMuted(response, request)
		if response.Code != tc.status {
			t.Fatalf("%v mapped to %d", tc.err, response.Code)
		}
	}
}
func TestMineRequiresAuthentication(t *testing.T) {
	response := httptest.NewRecorder()
	NewCommunityHandler(&experienceServiceStub{}).GetCommunities(response, httptest.NewRequest(http.MethodGet, "/?scope=mine", nil))
	if response.Code != 401 {
		t.Fatalf("mine status: %d", response.Code)
	}
}
