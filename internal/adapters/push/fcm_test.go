package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"testing"
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
		if m.Token != "device-token" || m.Data["chat_id"] != "chat" || m.Data["recipient_id"] != "recipient" || m.Data["message_id"] != "message" || m.Android.Notification["channel_id"] != "root_messages" || m.Android.Priority != "HIGH" || m.Notification["body"] == "" {
			t.Errorf("unexpected payload: %+v", body)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	fcm := &FCM{client: server.Client(), endpoint: server.URL}
	if err := fcm.Send(context.Background(), domain.PushJob{Token: "device-token", ChatID: "chat", UserID: "recipient", MessageID: "message"}); err != nil {
		t.Fatal(err)
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
