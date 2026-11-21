package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

var privateUserFields = []string{"email", "dob", "documentId", "passwordHash", "country", "createdAt", "updatedAt"}

func sampleUser() domain.User {
	dob, doc, country, avatar := "1999-01-01", "12345678", "AR", "https://cdn.example/a.jpg"
	return domain.User{
		ID: "u1", Email: "secret@example.com", PasswordHash: "hash", Name: "Ana", Username: "ana",
		Role: "ADMIN", AvatarURL: &avatar, Dob: &dob, DocumentID: &doc, Country: &country,
		Followers: []string{"u2", "u3"}, Following: []string{"u4"}, IsKycVerified: true,
	}
}

func assertNoFields(t *testing.T, obj map[string]interface{}, fields []string) {
	t.Helper()
	for _, f := range fields {
		if _, ok := obj[f]; ok {
			t.Errorf("field %q must not be exposed, got %v", f, obj[f])
		}
	}
}

func TestSearchUsersExposesOnlyPublicFields(t *testing.T) {
	handler := NewSearchHandler(searchStub{[]domain.User{sampleUser()}}, &bannerS3Stub{})
	req := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewBufferString(`{"query":"a","type":"users"}`))
	rec := httptest.NewRecorder()
	handler.Search(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("secret@example.com")) {
		t.Fatalf("email leaked in body: %s", rec.Body.String())
	}
	var body struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(body.Results))
	}
	got := body.Results[0]
	assertNoFields(t, got, append(privateUserFields, "role", "followers", "following"))
	want := map[string]interface{}{"id": "u1", "name": "Ana", "username": "ana", "avatarUrl": "https://cdn.example/a.jpg", "isKycVerified": true}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

type profileUserService struct {
	ports.UserService
	user domain.User
}

func (s *profileUserService) GetUserByUsername(context.Context, string, string) (*domain.User, bool, error) {
	u := s.user
	return &u, false, nil
}

func getProfile(t *testing.T, viewerID string) map[string]interface{} {
	t.Helper()
	h := NewUserHandler(&profileUserService{user: sampleUser()}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/users/ana", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", "ana")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if viewerID != "" {
		ctx = context.WithValue(ctx, UserIDKey, viewerID)
	}
	rec := httptest.NewRecorder()
	h.GetUser(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		User map[string]interface{} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.User
}

func TestGetUserHidesPrivateFieldsFromOthers(t *testing.T) {
	for name, viewer := range map[string]string{"anonymous": "", "other user": "u9"} {
		t.Run(name, func(t *testing.T) {
			got := getProfile(t, viewer)
			assertNoFields(t, got, privateUserFields)
			if got["username"] != "ana" || got["isKycVerified"] != true {
				t.Errorf("public fields missing: %v", got)
			}
			if got["followersCount"] != float64(2) || got["followingCount"] != float64(1) {
				t.Errorf("counts = %v / %v", got["followersCount"], got["followingCount"])
			}
		})
	}
}

func TestGetUserShowsOwnerTheirPrivateFields(t *testing.T) {
	got := getProfile(t, "u1")
	if got["email"] != "secret@example.com" || got["documentId"] != "12345678" {
		t.Errorf("owner should see their own private fields, got %v", got)
	}
	if _, ok := got["passwordHash"]; ok {
		t.Error("password hash must never be serialized")
	}
}
