package email

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryProviders(t *testing.T) {
	for _, provider := range []string{"resend", "brevo"} {
		t.Run(provider, func(t *testing.T) {
			s, err := NewTransactional(provider, "test-key", "Root <root@example.com>")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect email request")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if provider == "resend" {
					if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Idempotency-Key") != "password-reset-delivery" {
						t.Error("missing auth/idempotency")
					}
					if !strings.Contains(body["text"].(string), "#token=test-token") {
						t.Error("missing reset link")
					}
					w.Write([]byte(`{"id":"receipt"}`))
				} else {
					if r.Header.Get("api-key") != "test-key" || body["headers"].(map[string]any)["idempotencyKey"] != uuid.NewSHA1(uuid.NameSpaceURL, []byte("password-reset-delivery")).String() {
						t.Error("missing auth/idempotency")
					}
					if !strings.Contains(body["htmlContent"].(string), "#token=test-token") {
						t.Error("missing reset link")
					}
					w.WriteHeader(201)
					w.Write([]byte(`{"messageId":"receipt"}`))
				}
			}))
			defer server.Close()
			s.endpoint = server.URL
			if err := s.SendPasswordReset(context.Background(), "person@example.com", "https://root.example.com/reset-password#token=test-token", "delivery"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBrevoIdempotentRetry(t *testing.T) {
	for _, item := range []struct {
		body     string
		accepted bool
	}{
		{`{"code":"duplicate_parameter","message":"idempotencyKey already used"}`, true},
		{`{"code":"duplicate_parameter","message":"duplicate sender"}`, false},
		{`{"code":"invalid_parameter","message":"invalid idempotencyKey"}`, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); w.Write([]byte(item.body)) }))
		s, _ := NewTransactional("brevo", "test-key", "root@example.com")
		s.endpoint = server.URL
		err := s.SendPasswordReset(context.Background(), "person@example.com", "https://root.example.com/reset-password#token=private", "delivery")
		server.Close()
		if (err == nil) != item.accepted {
			t.Fatal("incorrect idempotency receipt classification")
		}
	}
}

func TestRecoveryEmailRejectsFailuresWithoutLeakingData(t *testing.T) {
	for _, status := range []int{401, 429, 500, 302, 200} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"secret-token person@example.com"}`))
		}))
		s, _ := NewTransactional("resend", "test-key", "root@example.com")
		s.endpoint = server.URL
		err := s.SendPasswordReset(context.Background(), "person@example.com", "https://root.example.com/#token=secret-token", "delivery")
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "person@example.com") {
			t.Fatal("failure must not report delivery or echo sensitive provider content")
		}
	}
	s, err := NewTransactional("", "", "")
	if s != nil || err != nil {
		t.Fatal("unselected provider must be disabled")
	}
	for _, config := range [][3]string{{"unknown", "key", "root@example.com"}, {"resend", "", "root@example.com"}, {"resend", "key", "invalid"}, {"", "key", "root@example.com"}, {"resend", "key\r\n", "root@example.com"}} {
		if _, err := NewTransactional(config[0], config[1], config[2]); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
