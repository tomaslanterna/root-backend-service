package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFCMPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid request")
		}
		var body struct {
			Message struct {
				Token        string
				Notification map[string]string
				Data         map[string]string
				Android      struct {
					Priority, TTL string
					Notification  map[string]string
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		m := body.Message
		if m.Token != "device-token" || m.Data["chat_id"] != "chat" || m.Data["recipient_id"] != "recipient" || m.Data["message_id"] != "message" || m.Android.Priority != "HIGH" || m.Android.TTL != "86400s" || m.Data["title"] != "María" || m.Data["body"] != "Nos vemos en la fiesta 🎉" || m.Notification != nil || m.Android.Notification != nil {
			t.Errorf("unexpected payload: %+v", body)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	fcm := &FCM{client: server.Client(), endpoint: server.URL}
	if err := fcm.Send(context.Background(), domain.PushJob{Token: "device-token", ChatID: "chat", UserID: "recipient", MessageID: "message", SenderName: "María", MessageType: domain.MessageTypeText, Content: "Nos vemos en la fiesta 🎉"}); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationPreview(t *testing.T) {
	for _, test := range []struct {
		name, text, want string
		limit            int
	}{
		{"empty", " \n\t", "fallback", 240},
		{"whitespace", " Hola\n  María\t🎉 ", "Hola María 🎉", 240},
		{"unicode", "á🎉é🎈", "á🎉é…", 3},
		{"exact limit", "á🎉é", "á🎉é", 3},
		{"long", strings.Repeat("🎉", 300), strings.Repeat("🎉", 240) + "…", 240},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := notificationPreview(test.text, "fallback", test.limit)
			if got != test.want || !utf8.ValidString(got) {
				t.Fatalf("invalid preview: %q", got)
			}
		})
	}
}

func TestFCMPreviewFallbacks(t *testing.T) {
	for _, test := range []struct {
		name string
		job  domain.PushJob
		body string
	}{
		{"photo", domain.PushJob{MessageType: domain.MessageTypeImage, Content: "https://private.example/photo"}, "📷 Envió una foto"},
		{"empty text", domain.PushJob{MessageType: domain.MessageTypeText, Content: " \n"}, "Tenés un mensaje nuevo."},
		{"unknown type", domain.PushJob{Content: "https://private.example/content"}, "Tenés un mensaje nuevo."},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Message struct{ Data map[string]string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Message.Data["title"] != "Nuevo mensaje" || body.Message.Data["body"] != test.body {
					t.Error("unexpected fallback preview")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			if err := (&FCM{client: server.Client(), endpoint: server.URL}).Send(context.Background(), test.job); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFCMOnlyExpiresUnregisteredTokens(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		expired    bool
	}{
		{"unregistered", `{"error":{"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, 404, true},
		{"bad credentials", `{"error":{"status":"PERMISSION_DENIED"}}`, 403, false},
		{"bad payload", `{"error":{"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`, 400, false},
		{"outage", `unavailable`, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := (&FCM{client: server.Client(), endpoint: server.URL}).Send(context.Background(), domain.PushJob{})
			if err == nil || errors.Is(err, domain.ErrPushTokenExpired) != test.expired {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
