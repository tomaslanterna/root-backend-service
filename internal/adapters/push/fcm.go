package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"io"
	"net/http"
	"net/url"
	"root-backend-service/internal/core/domain"
	"strings"
	"time"
)

type FCM struct {
	client   *http.Client
	endpoint string
}

// Credentials stay on the server; OAuth access tokens are refreshed automatically.
func NewFCM(ctx context.Context, project string) (*FCM, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second})
	credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, fmt.Errorf("FCM credentials: %w", err)
	}
	if project == "" {
		project = credentials.ProjectID
	}
	if project == "" {
		return nil, fmt.Errorf("FCM project ID is required")
	}
	client := oauth2.NewClient(ctx, credentials.TokenSource)
	client.Timeout = 10 * time.Second
	return &FCM{client: client, endpoint: "https://fcm.googleapis.com/v1/projects/" + url.PathEscape(project) + "/messages:send"}, nil
}

// Previews are bounded by Unicode characters to keep the FCM payload small without
// breaking accents or emoji. Android's lock-screen settings control their visibility.
func notificationPreview(value, fallback string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func (f *FCM) Send(ctx context.Context, job domain.PushJob) error {
	title := notificationPreview(job.SenderName, "Nuevo mensaje", 80)
	bodyText := "Tenés un mensaje nuevo."
	switch job.MessageType {
	case domain.MessageTypeText:
		bodyText = notificationPreview(job.Content, bodyText, 240)
	case domain.MessageTypeImage:
		// Do not expose private image URLs in notifications.
		bodyText = "📷 Envió una foto"
	}
	payload := map[string]any{"message": map[string]any{
		"token": job.Token,
		// The native Android service renders the bundled Root logo even in the
		// background. A notification payload would bypass that service in FCM.
		"data":    map[string]string{"type": "chat.message", "chat_id": job.ChatID, "message_id": job.MessageID, "recipient_id": job.UserID, "title": title, "body": bodyText},
		"android": map[string]any{"priority": "HIGH", "ttl": "86400s"},
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("FCM request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	var result struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&result); err != nil {
		return fmt.Errorf("FCM HTTP %d", res.StatusCode)
	}
	for _, detail := range result.Error.Details {
		if detail.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && detail.Code == "UNREGISTERED" {
			return domain.ErrPushTokenExpired
		}
	}
	// Do not delete tokens on a project/credential/payload error and never log tokens.
	return fmt.Errorf("FCM HTTP %d (%s)", res.StatusCode, result.Error.Status)
}
