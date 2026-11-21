package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type avatarUserService struct {
	ports.UserService
	saved string
}

func (s *avatarUserService) UpdateAvatar(_ context.Context, userID, avatar string) (*domain.User, error) {
	s.saved = avatar
	return &domain.User{ID: userID, AvatarURL: &avatar}, nil
}

type avatarStorage struct {
	url      string
	uploaded string
}

func (s *avatarStorage) UploadToS3(_ context.Context, _ io.Reader, key, _ string) (string, error) {
	s.uploaded = key
	return key, nil
}
func (s *avatarStorage) GenerateViewUrl(_ context.Context, key string, _ time.Duration) (string, error) {
	return s.url + key, nil
}
func (s *avatarStorage) GetObject(context.Context, string) ([]byte, error) { return nil, nil }
func (s *avatarStorage) GetObjectLimited(context.Context, string, int64) ([]byte, error) {
	return nil, nil
}

func avatarRequest(t *testing.T, payload []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("image", "me.jpg")
	part.Write(payload)
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req.WithContext(context.WithValue(req.Context(), UserIDKey, "user-1"))
}

// Minimal JPEG header: http.DetectContentType only needs the magic bytes.
var jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}, bytes.Repeat([]byte{0}, 64)...)

func TestUpdateAvatarStoresStableCDNURL(t *testing.T) {
	svc := &avatarUserService{}
	storage := &avatarStorage{url: "https://cdn.example.com/"}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, storage).UpdateAvatar(rec, avatarRequest(t, jpegBytes))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if storage.uploaded == "" || svc.saved != "https://cdn.example.com/"+storage.uploaded {
		t.Fatalf("expected the permanent CDN url to be saved, got %q (key %q)", svc.saved, storage.uploaded)
	}
	var user domain.User
	json.NewDecoder(rec.Body).Decode(&user)
	if user.AvatarURL == nil || *user.AvatarURL != svc.saved {
		t.Fatalf("response avatar = %v", user.AvatarURL)
	}
}

func TestUpdateAvatarStoresKeyWhenURLIsSigned(t *testing.T) {
	svc := &avatarUserService{}
	storage := &avatarStorage{url: "https://bucket.s3.amazonaws.com/?X-Amz-Signature=abc&key="}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, storage).UpdateAvatar(rec, avatarRequest(t, jpegBytes))

	if rec.Code != http.StatusOK || svc.saved != storage.uploaded {
		t.Fatalf("expected the S3 key to be saved (urls expire), got %q (status %d)", svc.saved, rec.Code)
	}
}

func TestUpdateAvatarRejectsNonImages(t *testing.T) {
	svc := &avatarUserService{}
	rec := httptest.NewRecorder()
	NewUserHandler(svc, &avatarStorage{}).UpdateAvatar(rec, avatarRequest(t, []byte("<html>not an image</html>")))

	if rec.Code != http.StatusUnsupportedMediaType || svc.saved != "" {
		t.Fatalf("status = %d, saved = %q", rec.Code, svc.saved)
	}
}

func TestUpdateAvatarRequiresAuthentication(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users/me/avatar", nil)
	NewUserHandler(&avatarUserService{}, &avatarStorage{}).UpdateAvatar(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}
