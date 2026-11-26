package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type accountServiceFake struct {
	deleted []string
	err     error
}

func (f *accountServiceFake) DeleteAccount(_ context.Context, userID string) error {
	f.deleted = append(f.deleted, userID)
	return f.err
}

type blockServiceFake struct {
	ports.BlockService
	err error
}

func (f *blockServiceFake) Block(context.Context, string, string) error   { return f.err }
func (f *blockServiceFake) Unblock(context.Context, string, string) error { return f.err }
func (f *blockServiceFake) List(context.Context, string) ([]domain.BlockedUser, error) {
	return []domain.BlockedUser{}, f.err
}

type reportServiceFake struct {
	created bool
	err     error
	got     domain.ReportInput
}

func (f *reportServiceFake) Create(_ context.Context, _ string, input domain.ReportInput) (string, bool, error) {
	f.got = input
	return "report-1", f.created, f.err
}

func moderationRequest(method, path, body, userID string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return req.WithContext(ctx)
}

func TestDeleteMe(t *testing.T) {
	accounts := &accountServiceFake{}
	h := NewModerationHandler(accounts, &blockServiceFake{}, &reportServiceFake{}, nil)

	rec := httptest.NewRecorder()
	h.DeleteMe(rec, moderationRequest(http.MethodDelete, "/v1/users/me", "", "user-1", nil))
	if rec.Code != http.StatusNoContent || len(accounts.deleted) != 1 || accounts.deleted[0] != "user-1" {
		t.Fatalf("status=%d deleted=%v", rec.Code, accounts.deleted)
	}

	rec = httptest.NewRecorder()
	h.DeleteMe(rec, moderationRequest(http.MethodDelete, "/v1/users/me", "", "", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}

	accounts.err = domain.ErrUserNotFound
	rec = httptest.NewRecorder()
	h.DeleteMe(rec, moderationRequest(http.MethodDelete, "/v1/users/me", "", "user-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("already deleted status=%d", rec.Code)
	}
}

func TestBlockStatuses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, http.StatusNoContent},
		{domain.ErrCannotBlockSelf, http.StatusBadRequest},
		{domain.ErrUserNotFound, http.StatusNotFound},
	} {
		h := NewModerationHandler(&accountServiceFake{}, &blockServiceFake{err: tc.err}, &reportServiceFake{}, nil)
		rec := httptest.NewRecorder()
		h.Block(rec, moderationRequest(http.MethodPost, "/v1/users/ana/block", "", "me", map[string]string{"username": "ana"}))
		if rec.Code != tc.want {
			t.Fatalf("err %v: status=%d want %d", tc.err, rec.Code, tc.want)
		}
	}
}

func TestListBlocksReturnsData(t *testing.T) {
	h := NewModerationHandler(&accountServiceFake{}, &blockServiceFake{}, &reportServiceFake{}, nil)
	rec := httptest.NewRecorder()
	h.ListBlocks(rec, moderationRequest(http.MethodGet, "/v1/users/me/blocks", "", "me", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateReportStatuses(t *testing.T) {
	body := `{"targetType":"post","targetId":"6f1c1d2e-3b4a-4c5d-8e9f-0a1b2c3d4e5f","reason":"spam","details":"publicidad"}`
	for _, tc := range []struct {
		name    string
		body    string
		service *reportServiceFake
		want    int
	}{
		{"new", body, &reportServiceFake{created: true}, http.StatusCreated},
		{"repeated", body, &reportServiceFake{}, http.StatusOK},
		{"invalid", body, &reportServiceFake{err: domain.ErrInvalidReport}, http.StatusBadRequest},
		{"missing target", body, &reportServiceFake{err: domain.ErrReportTargetNotFound}, http.StatusNotFound},
		{"unknown field", `{"targetType":"post","communityId":"x"}`, &reportServiceFake{}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewModerationHandler(&accountServiceFake{}, &blockServiceFake{}, tc.service, nil)
			rec := httptest.NewRecorder()
			h.CreateReport(rec, moderationRequest(http.MethodPost, "/v1/reports", tc.body, "me", nil))
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusCreated && (tc.service.got.TargetType != domain.ReportTargetPost || tc.service.got.Details != "publicidad") {
				t.Fatalf("service got %+v", tc.service.got)
			}
		})
	}
}

// ---------- Perfil y seguir con bloqueos ----------

type blockedProfileService struct {
	ports.UserService
	byViewer, byTarget bool
	followErr          error
}

func (s *blockedProfileService) GetUserByUsername(context.Context, string, string) (*domain.User, bool, error) {
	return &domain.User{ID: "target", Username: "ana", Followers: []string{}, Following: []string{}}, false, nil
}

func (s *blockedProfileService) BlockState(context.Context, string, string) (bool, bool, error) {
	return s.byViewer, s.byTarget, nil
}

func (s *blockedProfileService) FollowStatus(context.Context, string, *domain.User) (string, error) {
	return domain.FollowStatusNone, nil
}

func (s *blockedProfileService) FollowUser(context.Context, string, string) (string, error) {
	return "", s.followErr
}

func TestGetUserWithBlocks(t *testing.T) {
	rec := httptest.NewRecorder()
	NewUserHandler(&blockedProfileService{byViewer: true}, nil).
		GetUser(rec, moderationRequest(http.MethodGet, "/v1/users/ana", "", "me", map[string]string{"username": "ana"}))
	var body struct {
		IsBlocked      bool `json:"isBlocked"`
		CanViewContent bool `json:"canViewContent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || !body.IsBlocked || body.CanViewContent {
		t.Fatalf("blocked by me: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	NewUserHandler(&blockedProfileService{byTarget: true}, nil).
		GetUser(rec, moderationRequest(http.MethodGet, "/v1/users/ana", "", "me", map[string]string{"username": "ana"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("blocked by them must look missing: status=%d", rec.Code)
	}
}

func TestFollowBlockedUserReturnsBlocked(t *testing.T) {
	rec := httptest.NewRecorder()
	NewUserHandler(&blockedProfileService{followErr: domain.ErrBlocked}, nil).
		FollowUser(rec, moderationRequest(http.MethodPost, "/v1/users/ana/follow", "", "me", map[string]string{"username": "ana"}))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"blocked"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Registro con términos ----------

type registerAuthService struct {
	ports.AuthService
	called       bool
	termsVersion string
}

func (s *registerAuthService) Register(_ context.Context, _, _, _, _, termsVersion string, _ *string, _ *string, _ *string) (*domain.User, error) {
	s.called = true
	s.termsVersion = termsVersion
	return &domain.User{ID: "new"}, nil
}

func TestRegisterRequiresTerms(t *testing.T) {
	auth := &registerAuthService{}
	rec := httptest.NewRecorder()
	NewAuthHandler(auth).Register(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/register", strings.NewReader(`{"name":"Ana Paz","email":"a@b.c","password":"12345678","role":"ADMIN"}`)))
	if rec.Code != http.StatusBadRequest || auth.called {
		t.Fatalf("without terms: status=%d called=%v", rec.Code, auth.called)
	}

	rec = httptest.NewRecorder()
	NewAuthHandler(auth).Register(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/register", strings.NewReader(`{"name":"Ana Paz","email":"a@b.c","password":"12345678","acceptedTerms":true,"termsVersion":"old"}`)))
	if rec.Code != http.StatusCreated || auth.termsVersion != domain.CurrentTermsVersion {
		t.Fatalf("with terms: status=%d version=%q", rec.Code, auth.termsVersion)
	}
}
