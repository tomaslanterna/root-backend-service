package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"testing"

	"github.com/go-chi/chi/v5"
)

type communityServiceForHandlerStub struct {
	filter      domain.CommunityFilter
	currentUser string
	joinCalls   int
	leaveCalls  int
}

func (s *communityServiceForHandlerStub) GetCommunities(_ context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	s.filter = filter
	s.currentUser = currentUserID
	return []domain.Community{{ID: "community-1"}}, 7, nil
}
func (s *communityServiceForHandlerStub) GetCommunity(context.Context, string, string) (*domain.Community, error) {
	return &domain.Community{ID: "community-1"}, nil
}
func (s *communityServiceForHandlerStub) JoinCommunity(context.Context, string, string) (int, error) {
	s.joinCalls++
	return 5, nil
}
func (s *communityServiceForHandlerStub) LeaveCommunity(context.Context, string, string) (int, error) {
	s.leaveCalls++
	return 4, nil
}
func (s *communityServiceForHandlerStub) GetUserCommunities(context.Context, string, string) ([]domain.Community, error) {
	return []domain.Community{}, nil
}
func (s *communityServiceForHandlerStub) GetCurrentUserCommunities(context.Context, string) ([]domain.Community, error) {
	return []domain.Community{}, nil
}

func communityRequestWithRouteAndUser(request *http.Request, communityID, userID string) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", communityID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return request.WithContext(ctx)
}

func TestGetCommunitiesParsesCombinedFiltersAndRealTotal(t *testing.T) {
	service := &communityServiceForHandlerStub{}
	handler := NewCommunityHandler(service)
	request := httptest.NewRequest(http.MethodGet, "/v1/communities?country=uy&category=electronica&department=montevideo&query=festival&limit=3&offset=2", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserIDKey, "viewer-1"))
	response := httptest.NewRecorder()

	handler.GetCommunities(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.filter.Country != "uy" || service.filter.Category != "electronica" || service.filter.Department != "montevideo" || service.filter.Query != "festival" || service.filter.Limit != 3 || service.filter.Offset != 2 {
		t.Fatalf("unexpected filters: %+v", service.filter)
	}
	var payload struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if payload.Meta.Total != 7 {
		t.Fatalf("expected total 7, got %d", payload.Meta.Total)
	}
}

func TestGetCommunitiesRejectsInvalidPagination(t *testing.T) {
	handler := NewCommunityHandler(&communityServiceForHandlerStub{})
	response := httptest.NewRecorder()
	handler.GetCommunities(response, httptest.NewRequest(http.MethodGet, "/v1/communities?limit=invalid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestMembershipHandlersRequireAuthenticatedUser(t *testing.T) {
	service := &communityServiceForHandlerStub{}
	handler := NewCommunityHandler(service)
	for name, method := range map[string]func(http.ResponseWriter, *http.Request){"join": handler.JoinCommunity, "leave": handler.LeaveCommunity} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := communityRequestWithRouteAndUser(httptest.NewRequest(http.MethodPost, "/", nil), "community-1", "")
			method(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", response.Code)
			}
		})
	}
}
