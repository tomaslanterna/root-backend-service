package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSOriginAllowed(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://preview.example.com, *")
	cases := map[string]bool{
		"https://www.estoesroot.com":      true,
		"https://estoesroot.com":          true,
		"https://root-web-app.vercel.app": true,
		"capacitor://localhost":           true,
		"https://localhost":               true,
		"http://localhost:3000":           true,
		"https://preview.example.com":     true,
		"https://evil.example.com":        false,
		"https://www.estoesroot.com.evil": false,
		"http://www.estoesroot.com":       false,
		"null":                            false,
		"":                                false,
		"*":                               false,
	}
	for origin, want := range cases {
		if got := corsOriginAllowed(origin); got != want {
			t.Errorf("corsOriginAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestRouterCORSRejectsUnknownOriginsAndNeverSendsCredentials(t *testing.T) {
	router := NewRouter(RouterConfig{})
	preflight := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/health", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	ok := preflight("https://www.estoesroot.com")
	if got := ok.Header().Get("Access-Control-Allow-Origin"); got != "https://www.estoesroot.com" {
		t.Fatalf("allowed origin not echoed: %q", got)
	}
	if got := ok.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("credentials must not be allowed: %q", got)
	}
	if got := preflight("https://evil.example.com").Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin was allowed: %q", got)
	}
}
