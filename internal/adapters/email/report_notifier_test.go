package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"root-backend-service/internal/core/domain"
)

func TestNewReportNotifierIsOptional(t *testing.T) {
	sender, err := NewTransactional("resend", "test-key", "Root <root@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := NewReportNotifier(nil, "soporte@example.com"); n != nil || err != nil {
		t.Fatalf("without provider: %v %v", n, err)
	}
	if n, err := NewReportNotifier(sender, " "); n != nil || err != nil {
		t.Fatalf("without recipient: %v %v", n, err)
	}
	if _, err := NewReportNotifier(sender, "not an email"); err == nil {
		t.Fatal("invalid recipient accepted")
	}
}

func TestSendReportNotificationEscapesContent(t *testing.T) {
	sender, err := NewTransactional("resend", "test-key", "Root <root@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Header.Get("Idempotency-Key") != "content-report-r1" {
			t.Error("missing idempotency key")
		}
		to := body["to"].([]any)
		markup := body["html"].(string)
		if len(to) != 1 || to[0] != "soporte@example.com" || !strings.Contains(body["subject"].(string), "Mensaje de chat") {
			t.Errorf("unexpected envelope: %v", body)
		}
		if strings.Contains(markup, "<script>") || !strings.Contains(markup, "&lt;script&gt;") || !strings.Contains(body["text"].(string), "@beto") {
			t.Error("content not escaped or missing")
		}
		w.Write([]byte(`{"id":"receipt"}`))
	}))
	defer server.Close()
	sender.endpoint = server.URL
	notifier, err := NewReportNotifier(sender, "Soporte <soporte@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	report := domain.ContentReport{ID: "r1", TargetType: domain.ReportTargetMessage, Reason: "abuse", TargetUsername: "beto", ContentSnapshot: "<script>alert(1)</script>"}
	if err := notifier.SendReportNotification(context.Background(), report); err != nil {
		t.Fatal(err)
	}
}
