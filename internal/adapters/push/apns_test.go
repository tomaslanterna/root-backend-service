package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func generateTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	return key
}

func TestAPNSPayloadAndHeaders(t *testing.T) {
	testKey := generateTestKey(t)
	tokenSeen := ""
	authSeen := ""
	topicSeen := ""
	pushTypeSeen := ""

	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			tokenSeen = strings.TrimPrefix(r.URL.Path, "/3/device/")
			authSeen = r.Header.Get("Authorization")
			topicSeen = r.Header.Get("apns-topic")
			pushTypeSeen = r.Header.Get("apns-push-type")

			var body struct {
				Aps struct {
					Alert struct {
						Title string `json:"title"`
						Body  string `json:"body"`
					} `json:"alert"`
					Sound string `json:"sound"`
					Badge int    `json:"badge"`
				} `json:"aps"`
				Type        string `json:"type"`
				ChatID      string `json:"chat_id"`
				MessageID   string `json:"message_id"`
				RecipientID string `json:"recipient_id"`
			}

			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode payload: %v", err)
			}

			if body.Aps.Alert.Title != "Lucía" {
				t.Errorf("expected title 'Lucía', got %q", body.Aps.Alert.Title)
			}
			if body.Aps.Alert.Body != "Nos vemos en la pista 🔥" {
				t.Errorf("expected body 'Nos vemos en la pista 🔥', got %q", body.Aps.Alert.Body)
			}
			if body.Aps.Sound != "default" || body.Aps.Badge != 1 {
				t.Errorf("unexpected aps settings: %+v", body.Aps)
			}
			if body.ChatID != "chat-123" || body.RecipientID != "user-456" || body.Type != "chat.message" {
				t.Errorf("unexpected custom payload: %+v", body)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	apns := &APNS{
		client:     mockClient,
		endpoint:   "https://api.sandbox.push.apple.com",
		keyID:      "TESTKEY123",
		teamID:     "TESTTEAM99",
		bundleID:   "com.emnetwork.root",
		privateKey: testKey,
	}

	job := domain.PushJob{
		Token:       "740f4707bebcf74f9b7c25d48e3358945f6aa01da5ddb387462c7eaf61bb78ad",
		Platform:    "ios",
		ChatID:      "chat-123",
		UserID:      "user-456",
		MessageID:   "msg-789",
		SenderName:  "Lucía",
		MessageType: domain.MessageTypeText,
		Content:     "Nos vemos en la pista 🔥",
	}

	if err := apns.Send(context.Background(), job); err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}

	if tokenSeen != job.Token {
		t.Errorf("expected device token in path, got %q", tokenSeen)
	}
	if !strings.HasPrefix(authSeen, "bearer ") {
		t.Errorf("expected bearer token in Authorization, got %q", authSeen)
	}
	if topicSeen != "com.emnetwork.root" {
		t.Errorf("expected apns-topic 'com.emnetwork.root', got %q", topicSeen)
	}
	if pushTypeSeen != "alert" {
		t.Errorf("expected apns-push-type 'alert', got %q", pushTypeSeen)
	}
}

func TestAPNSUnregisteredTokenReturnsExpired(t *testing.T) {
	testKey := generateTestKey(t)
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusGone,
				Body:       io.NopCloser(strings.NewReader(`{"reason":"Unregistered","timestamp":1700000000}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	apns := &APNS{
		client:     mockClient,
		endpoint:   "https://api.sandbox.push.apple.com",
		keyID:      "TESTKEY123",
		teamID:     "TESTTEAM99",
		bundleID:   "com.emnetwork.root",
		privateKey: testKey,
	}

	err := apns.Send(context.Background(), domain.PushJob{Token: "bad-token"})
	if !errors.Is(err, domain.ErrPushTokenExpired) {
		t.Fatalf("expected ErrPushTokenExpired, got %v", err)
	}
}

func TestAPNSImageFallback(t *testing.T) {
	testKey := generateTestKey(t)
	var bodyText string
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body struct {
				Aps struct {
					Alert struct {
						Body string `json:"body"`
					} `json:"alert"`
				} `json:"aps"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodyText = body.Aps.Alert.Body
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	apns := &APNS{
		client:     mockClient,
		endpoint:   "https://api.sandbox.push.apple.com",
		keyID:      "TESTKEY123",
		teamID:     "TESTTEAM99",
		bundleID:   "com.emnetwork.root",
		privateKey: testKey,
	}

	err := apns.Send(context.Background(), domain.PushJob{
		Token:       "tok",
		MessageType: domain.MessageTypeImage,
		Content:     "https://private-bucket/secret.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bodyText != "📷 Envió una foto" {
		t.Errorf("expected photo fallback preview, got %q", bodyText)
	}
}
