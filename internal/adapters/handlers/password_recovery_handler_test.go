package handlers

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"testing"
	"time"
)

type recoveryHandlerFake struct {
	ports.PasswordRecoveryService
	err   error
	email string
	ip    string
	calls int
}

func (f *recoveryHandlerFake) RequestReset(_ context.Context, email, ip string) error {
	f.email = email
	f.ip = ip
	f.calls++
	return f.err
}
func (f *recoveryHandlerFake) ResetPassword(_ context.Context, _, _, _, ip string) error {
	f.ip = ip
	f.calls++
	return f.err
}
func TestRecoveryHTTPValidationAndErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		err    error
		status int
	}{
		{`{"email":"a@example.com"}`, nil, 202},
		{`{"email":"absent@example.com"}`, nil, 202},
		{`{"email":"oauth@example.com"}`, nil, 202},
		{`{"email":"a@example.com","userId":"other"}`, nil, 400},
		{`{"email":"a@example.com"} {}`, nil, 400},
		{`{`, nil, 400},
		{`{"email":"` + strings.Repeat("a", 5000) + `"}`, nil, 400},
		{`{"email":"a@example.com"}`, domain.ErrRecoveryUnavailable, 503},
		{`{"email":"a@example.com"}`, domain.ErrAuthRateLimited, 429},
		{`{"email":"invalid"}`, domain.ErrInvalidRecoveryEmail, 400},
		{`{"email":"a@example.com"}`, errors.New("internal failure containing sensitive details"), 500},
	} {
		fake := &recoveryHandlerFake{err: tc.err}
		handler := NewPasswordRecoveryHandler(fake)
		r := httptest.NewRequest("POST", "/v1/auth/forgot-password", strings.NewReader(tc.body))
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("X-Forwarded-For", "attacker-chosen")
		w := httptest.NewRecorder()
		handler.Request(w, r)
		if w.Code != tc.status {
			t.Fatalf("want %d got %d", tc.status, w.Code)
		}
		if strings.Contains(w.Body.String(), "sensitive details") || strings.Contains(w.Body.String(), "a@example.com") {
			t.Fatal("response leaked internals/email")
		}
		if fake.calls > 0 && fake.ip != "192.0.2.10" {
			t.Fatal("untrusted forwarded address bypasses limiter")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("recovery response must not be cached")
		}
	}
}
func TestResetHTTPNeverAcceptsArbitraryUserIdentity(t *testing.T) {
	for _, tc := range []struct {
		body   string
		err    error
		status int
	}{
		{`{"token":"secret","password":"Password123","confirmPassword":"Password123"}`, nil, 200},
		{`{"token":"secret","password":"Password123","confirmPassword":"Password123","userId":"other"}`, nil, 400},
		{`{"token":"secret","password":"Password123","confirmPassword":"Password123","email":"other@example.com"}`, nil, 400},
		{`{"token":"secret"}`, domain.ErrInvalidResetToken, 400},
		{`{"token":"secret"}`, domain.ErrInvalidPassword, 400},
		{`{"token":"secret"}`, domain.ErrAuthRateLimited, 429},
	} {
		handler := NewPasswordRecoveryHandler(&recoveryHandlerFake{err: tc.err})
		w := httptest.NewRecorder()
		handler.Reset(w, httptest.NewRequest("POST", "/v1/auth/reset-password", strings.NewReader(tc.body)))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unexpected reset response: %d", w.Code)
		}
	}
}
func sessionTestJWT(t *testing.T, version any, includeVersion bool) string {
	t.Helper()
	claims := jwt.MapClaims{"sub": "user", "exp": time.Now().Add(time.Hour).Unix()}
	if includeVersion {
		claims["sessionVersion"] = version
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("session-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func TestSessionRevocationRequiresFreshVersionAndPreservesAnonymousAccess(t *testing.T) {
	t.Setenv("JWT_SECRET", "session-test-secret")
	validate := ports.SessionValidator(func(_ context.Context, id string, version int64) error {
		if id != "user" || version != 1 {
			return domain.ErrInvalidSession
		}
		return nil
	})
	endpoint := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(UserIDKey) == "user" {
			w.WriteHeader(204)
		} else {
			w.WriteHeader(200)
		}
	})
	private := sessionValidationContext(validate)(AuthMiddleware(endpoint))
	public := sessionValidationContext(validate)(OptionalAuthMiddleware(endpoint))
	for _, tc := range []struct {
		token  string
		status int
	}{
		{"", 401}, {sessionTestJWT(t, nil, false), 401}, {sessionTestJWT(t, 0, true), 401},
		{sessionTestJWT(t, 1, true), 204}, {sessionTestJWT(t, "1", true), 401}, {sessionTestJWT(t, -1, true), 401}, {sessionTestJWT(t, 1.5, true), 401},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		private.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("private want %d got %d", tc.status, w.Code)
		}
		w = httptest.NewRecorder()
		public.ServeHTTP(w, r)
		expected := 200
		if tc.status == 204 {
			expected = 204
		}
		if w.Code != expected {
			t.Fatal("optional auth must degrade to anonymous, not trust revoked JWT")
		}
	}
	// Existing JWTs without the new claim remain valid before the first reset.
	validate = func(_ context.Context, _ string, version int64) error {
		if version != 0 {
			return domain.ErrInvalidSession
		}
		return nil
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+sessionTestJWT(t, nil, false))
	w := httptest.NewRecorder()
	sessionValidationContext(validate)(AuthMiddleware(endpoint)).ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("legacy session invalidated without a password reset")
	}
}
