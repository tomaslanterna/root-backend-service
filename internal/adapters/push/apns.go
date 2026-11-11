package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"root-backend-service/internal/core/domain"
	"sync"
	"time"
)

type APNS struct {
	client     *http.Client
	endpoint   string
	keyID      string
	teamID     string
	bundleID   string
	privateKey *ecdsa.PrivateKey

	tokenMu   sync.Mutex
	jwtToken  string
	tokenTime time.Time
}

type APNSConfig struct {
	KeyPath    string
	KeyID      string
	TeamID     string
	BundleID   string
	Production bool
	Client     *http.Client // Optional: for testing or custom transport
	Endpoint   string       // Optional: override endpoint for testing
}

func NewAPNS(cfg APNSConfig) (*APNS, error) {
	if cfg.KeyID == "" {
		return nil, errors.New("APNs Key ID is required")
	}
	if cfg.TeamID == "" {
		return nil, errors.New("APNs Team ID is required")
	}
	if cfg.BundleID == "" {
		cfg.BundleID = "com.emnetwork.root"
	}
	keyBytes, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read APNs key file: %w", err)
	}
	privateKey, err := parsePKCS8PrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse APNs private key: %w", err)
	}

	endpoint := "https://api.sandbox.push.apple.com"
	if cfg.Production {
		endpoint = "https://api.push.apple.com"
	}
	if cfg.Endpoint != "" {
		endpoint = cfg.Endpoint
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &APNS{
		client:     client,
		endpoint:   endpoint,
		keyID:      cfg.KeyID,
		teamID:     cfg.TeamID,
		bundleID:   cfg.BundleID,
		privateKey: privateKey,
	}, nil
}

func parsePKCS8PrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM data found in key file")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Fallback to SEC 1 EC private key if provided in that format
		ecKey, ecErr := x509.ParseECPrivateKey(block.Bytes)
		if ecErr != nil {
			return nil, fmt.Errorf("PKCS#8 error (%w), EC error (%v)", err, ecErr)
		}
		return ecKey, nil
	}
	ecdsaKey, ok := parsedKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not an ECDSA private key")
	}
	return ecdsaKey, nil
}

func (a *APNS) getAuthToken() (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// Apple allows tokens to be valid for up to 60 minutes. We refresh every 45 minutes.
	if a.jwtToken != "" && time.Since(a.tokenTime) < 45*time.Minute {
		return a.jwtToken, nil
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"alg":"ES256","kid":"%s"}`, a.keyID)))
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"iss":"%s","iat":%d}`, a.teamID, time.Now().Unix())))
	signingInput := header + "." + claims

	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, a.privateKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign APNs JWT: %w", err)
	}

	curveBits := a.privateKey.Curve.Params().BitSize
	keyBytes := (curveBits + 7) / 8

	signature := make([]byte, keyBytes*2)
	r.FillBytes(signature[:keyBytes])
	s.FillBytes(signature[keyBytes:])

	a.jwtToken = signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
	a.tokenTime = time.Now()
	return a.jwtToken, nil
}

func (a *APNS) Send(ctx context.Context, job domain.PushJob) error {
	title := notificationPreview(job.SenderName, "Nuevo mensaje", 80)
	bodyText := "Tenés un mensaje nuevo."
	switch job.MessageType {
	case domain.MessageTypeText:
		bodyText = notificationPreview(job.Content, bodyText, 240)
	case domain.MessageTypeImage:
		bodyText = "📷 Envió una foto"
	}

	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{
				"title": title,
				"body":  bodyText,
			},
			"sound": "default",
			"badge": 1,
		},
		"type":         "chat.message",
		"chat_id":      job.ChatID,
		"message_id":   job.MessageID,
		"recipient_id": job.UserID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	token, err := a.getAuthToken()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/3/device/%s", a.endpoint, job.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("apns-topic", a.bundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", fmt.Sprintf("%d", time.Now().Add(24*time.Hour).Unix()))
	req.Header.Set("Content-Type", "application/json")

	res, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("APNs request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusOK {
		return nil
	}

	var apnsError struct {
		Reason    string `json:"reason"`
		Timestamp int64  `json:"timestamp"`
	}
	responseBody, _ := io.ReadAll(http.MaxBytesReader(nil, res.Body, 4096))
	_ = json.Unmarshal(responseBody, &apnsError)

	if res.StatusCode == http.StatusGone || apnsError.Reason == "Unregistered" || apnsError.Reason == "BadDeviceToken" {
		return domain.ErrPushTokenExpired
	}

	if apnsError.Reason != "" {
		return fmt.Errorf("APNs HTTP %d (%s)", res.StatusCode, apnsError.Reason)
	}
	return fmt.Errorf("APNs HTTP %d", res.StatusCode)
}
