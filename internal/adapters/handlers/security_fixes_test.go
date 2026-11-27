package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"root-backend-service/internal/core/ports"
)

type webhookTransferStub struct {
	ports.TransferService
	calls int
	err   error
}

func (s *webhookTransferStub) HandleMercadoPagoWebhook(context.Context, string, string) error {
	s.calls++
	return s.err
}

func signMercadoPago(secret, id, requestID, ts string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("id:" + id + ";request-id:" + requestID + ";ts:" + ts + ";"))
	return "ts=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestMercadoPagoWebhookRejectsNonNumericIDs(t *testing.T) {
	svc := &webhookTransferStub{}
	rec := httptest.NewRecorder()
	NewTransferHandler(svc).MercadoPagoWebhook(rec, httptest.NewRequest(http.MethodPost, "/v1/webhooks/mercadopago?topic=payment&id=..%2F..%2Fusers", nil))
	if rec.Code != http.StatusBadRequest || svc.calls != 0 {
		t.Fatalf("status = %d, calls = %d", rec.Code, svc.calls)
	}
}

func TestMercadoPagoWebhookValidatesSignatureWhenSecretIsSet(t *testing.T) {
	t.Setenv("MERCADOPAGO_WEBHOOK_SECRET", "mp-secret")
	call := func(signature string) (int, int) {
		svc := &webhookTransferStub{}
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/mercadopago?type=payment&data.id=123456", nil)
		req.Header.Set("x-request-id", "req-1")
		if signature != "" {
			req.Header.Set("x-signature", signature)
		}
		rec := httptest.NewRecorder()
		NewTransferHandler(svc).MercadoPagoWebhook(rec, req)
		return rec.Code, svc.calls
	}
	if code, calls := call(""); code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unsigned: status %d calls %d", code, calls)
	}
	if code, calls := call(signMercadoPago("other", "123456", "req-1", "1700000000")); code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("bad signature: status %d calls %d", code, calls)
	}
	if code, calls := call(signMercadoPago("mp-secret", "123456", "req-1", "1700000000")); code != http.StatusOK || calls != 1 {
		t.Fatalf("valid signature: status %d calls %d", code, calls)
	}
}

func TestMercadoPagoWebhookDoesNotLeakInternalErrors(t *testing.T) {
	svc := &webhookTransferStub{err: context.DeadlineExceeded}
	rec := httptest.NewRecorder()
	NewTransferHandler(svc).MercadoPagoWebhook(rec, httptest.NewRequest(http.MethodPost, "/v1/webhooks/mercadopago?topic=payment&id=42", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "deadline") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTokensAreRejectedWithoutJWTSecret(t *testing.T) {
	// Antes se validaba contra "default_fallback_secret": un token firmado con ese valor no debe pasar.
	t.Setenv("JWT_SECRET", "")
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "admin", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("default_fallback_secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseToken(token); err == nil {
		t.Fatal("a token signed with the old fallback secret was accepted")
	}
}

func TestOldCrewMockEndpointsAreGone(t *testing.T) {
	router := NewRouter(RouterConfig{})
	for _, route := range []struct{ method, path string }{{http.MethodGet, "/v1/crews/deck"}, {http.MethodPost, "/v1/crews/swipe"}} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("%s %s still answers 200", route.method, route.path)
		}
	}
}

func TestPostImageUploadOnlyStoresRealImages(t *testing.T) {
	storage := &bannerS3Stub{}
	handler := NewPostHandler(nil, storage)

	rec := httptest.NewRecorder()
	handler.UploadImage(rec, multipartImagesRequest(t, http.MethodPost, "/v1/posts/image", "image", []byte("<svg onload=alert(1)>")))
	if rec.Code != http.StatusUnsupportedMediaType || storage.uploadedKey != "" {
		t.Fatalf("non-image accepted: status %d key %q", rec.Code, storage.uploadedKey)
	}

	gif := append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 32)...)
	for _, content := range [][]byte{bannerImageBytes, gif} {
		rec = httptest.NewRecorder()
		handler.UploadImage(rec, multipartImagesRequest(t, http.MethodPost, "/v1/posts/image", "image", content))
		if rec.Code != http.StatusOK || !strings.HasPrefix(storage.uploadedKey, "posts/user-1/") || strings.Contains(storage.uploadedKey, "evil") {
			t.Fatalf("status %d key %q body %s", rec.Code, storage.uploadedKey, rec.Body.String())
		}
	}
}
