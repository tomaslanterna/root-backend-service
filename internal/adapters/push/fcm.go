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

func (f *FCM) Send(ctx context.Context, job domain.PushJob) error {
	// Generic body intentionally avoids exposing private message text on a lock screen.
	payload := map[string]any{"message": map[string]any{
		"token":        job.Token,
		"notification": map[string]string{"title": "Nuevo mensaje en root", "body": "Tenés un mensaje nuevo. Tocá para abrir el chat."},
		"data":         map[string]string{"type": "chat.message", "chat_id": job.ChatID, "message_id": job.MessageID, "recipient_id": job.UserID},
		"android":      map[string]any{"priority": "HIGH", "ttl": "86400s", "notification": map[string]string{"channel_id": "root_messages", "tag": job.ChatID, "sound": "default"}},
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
