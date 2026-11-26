package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"root-backend-service/internal/core/domain"
	kycservice "root-backend-service/internal/services/kyc"
	s3service "root-backend-service/internal/services/s3"

	"github.com/go-chi/chi/v5"
)

type kycRepoFake struct {
	sessions    map[string]*domain.KycSession
	created     []*domain.KycSession
	updated     int
	lastQueried string
}

func (f *kycRepoFake) CreateSession(_ context.Context, s *domain.KycSession) error {
	f.created = append(f.created, s)
	return nil
}

func (f *kycRepoFake) GetSessionByID(_ context.Context, id string) (*domain.KycSession, error) {
	if s, ok := f.sessions[id]; ok {
		copy := *s
		return &copy, nil
	}
	return nil, errors.New("session not found")
}

func (f *kycRepoFake) UpdateSession(_ context.Context, s *domain.KycSession) error {
	f.updated++
	f.sessions[s.ID] = s
	return nil
}

func (f *kycRepoFake) GetLastSessionByUserID(_ context.Context, userID string) (*domain.KycSession, error) {
	f.lastQueried = userID
	return nil, errors.New("session not found")
}

type kycS3Fake struct {
	s3service.S3Service
	keys []string
}

func (f *kycS3Fake) UploadToS3(_ context.Context, _ io.Reader, key, _ string) (string, error) {
	f.keys = append(f.keys, key)
	return key, nil
}

type kycProviderFake struct{ calls int }

func (f *kycProviderFake) AnalyzeIdentity(context.Context, string, string, string, string, string, string, string) (*kycservice.KycResult, error) {
	f.calls++
	return &kycservice.KycResult{MatchScore: 10}, nil
}

func newKycTestHandler() (*KycHandler, *kycRepoFake, *kycS3Fake, *kycProviderFake) {
	repo := &kycRepoFake{sessions: map[string]*domain.KycSession{
		"kyc_sess_owner": {ID: "kyc_sess_owner", UserID: "owner", Status: "CREATED"},
	}}
	s3 := &kycS3Fake{}
	provider := &kycProviderFake{}
	return NewKycHandler(s3, provider, repo, nil), repo, s3, provider
}

func kycRequest(method, path, sessionID, userID string, body io.Reader, contentType string) *http.Request {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rctx := chi.NewRouteContext()
	if sessionID != "" {
		rctx.URLParams.Add("id", sessionID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return req.WithContext(ctx)
}

func kycMultipart(t *testing.T, field string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(field, "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("jpeg-bytes"))
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestKycRoutesRequireJWT(t *testing.T) {
	h, repo, s3, provider := newKycTestHandler()
	router := NewRouter(RouterConfig{KycHandler: h})
	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/kyc/sessions"},
		{http.MethodPost, "/v1/kyc/sessions/kyc_sess_owner/document"},
		{http.MethodPost, "/v1/kyc/sessions/kyc_sess_owner/face"},
		{http.MethodPost, "/v1/kyc/sessions/kyc_sess_owner/submit"},
		{http.MethodGet, "/v1/kyc/sessions/kyc_sess_owner/status"},
	}
	for _, route := range routes {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(route.method, route.path, strings.NewReader(`{"userId":"owner"}`)))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status %d", route.method, route.path, res.Code)
		}
	}
	if len(repo.created) != 0 || repo.updated != 0 || len(s3.keys) != 0 || provider.calls != 0 {
		t.Fatalf("unauthenticated requests reached the handler: %+v %v %d", repo, s3.keys, provider.calls)
	}
}

func TestKycWebhookStaysPublic(t *testing.T) {
	h, _, _, _ := newKycTestHandler()
	res := httptest.NewRecorder()
	NewRouter(RouterConfig{KycHandler: h}).ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/webhooks/kyc-provider", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d", res.Code)
	}
}

func TestKycCreateSessionUsesTokenUserAndIgnoresBody(t *testing.T) {
	h, repo, _, _ := newKycTestHandler()
	res := httptest.NewRecorder()
	h.CreateSession(res, kycRequest(http.MethodPost, "/v1/kyc/sessions", "", "me", strings.NewReader(`{"userId":"victim"}`), "application/json"))

	if res.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", res.Code, res.Body.String())
	}
	if len(repo.created) != 1 || repo.created[0].UserID != "me" || repo.lastQueried != "me" {
		t.Fatalf("session not created for the token user: created %+v, queried %q", repo.created, repo.lastQueried)
	}
}

func TestKycCreateSessionWithoutUserIs401(t *testing.T) {
	h, repo, _, _ := newKycTestHandler()
	res := httptest.NewRecorder()
	h.CreateSession(res, kycRequest(http.MethodPost, "/v1/kyc/sessions", "", "", strings.NewReader(`{"userId":"victim"}`), "application/json"))

	if res.Code != http.StatusUnauthorized || len(repo.created) != 0 {
		t.Fatalf("status %d, created %d", res.Code, len(repo.created))
	}
}

func TestKycSessionRoutesHideOtherUsersSessions(t *testing.T) {
	for _, sessionID := range []string{"kyc_sess_owner", "kyc_sess_missing"} {
		h, repo, s3, provider := newKycTestHandler()
		docBody, docType := kycMultipart(t, "frontImage")
		faceBody, faceType := kycMultipart(t, "faceMedia")
		cases := []struct {
			name    string
			handler http.HandlerFunc
			req     *http.Request
		}{
			{"document", h.UploadDocument, kycRequest(http.MethodPost, "/document", sessionID, "intruder", docBody, docType)},
			{"face", h.UploadFace, kycRequest(http.MethodPost, "/face", sessionID, "intruder", faceBody, faceType)},
			{"submit", h.SubmitSession, kycRequest(http.MethodPost, "/submit", sessionID, "intruder", nil, "")},
			{"status", h.GetStatus, kycRequest(http.MethodGet, "/status", sessionID, "intruder", nil, "")},
		}
		for _, tc := range cases {
			res := httptest.NewRecorder()
			tc.handler(res, tc.req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status %d", sessionID, tc.name, res.Code)
			}
		}
		if repo.updated != 0 || len(s3.keys) != 0 || provider.calls != 0 {
			t.Fatalf("%s: intruder caused side effects: updates %d, uploads %v, analyses %d", sessionID, repo.updated, s3.keys, provider.calls)
		}
	}
}

func TestKycOwnerCanUseTheirSession(t *testing.T) {
	h, repo, s3, provider := newKycTestHandler()

	faceBody, faceType := kycMultipart(t, "faceMedia")
	res := httptest.NewRecorder()
	h.UploadFace(res, kycRequest(http.MethodPost, "/face", "kyc_sess_owner", "owner", faceBody, faceType))
	if res.Code != http.StatusOK || len(s3.keys) != 1 || repo.sessions["kyc_sess_owner"].Status != "FACE_UPLOADED" {
		t.Fatalf("face upload: status %d, keys %v, session %+v", res.Code, s3.keys, repo.sessions["kyc_sess_owner"])
	}

	docBody, docType := kycMultipart(t, "frontImage")
	res = httptest.NewRecorder()
	h.UploadDocument(res, kycRequest(http.MethodPost, "/document", "kyc_sess_owner", "owner", docBody, docType))
	if res.Code != http.StatusOK || len(s3.keys) != 2 {
		t.Fatalf("document upload: status %d, keys %v", res.Code, s3.keys)
	}

	res = httptest.NewRecorder()
	h.SubmitSession(res, kycRequest(http.MethodPost, "/submit", "kyc_sess_owner", "owner", nil, ""))
	if res.Code != http.StatusAccepted || provider.calls != 1 {
		t.Fatalf("submit: status %d, analyses %d", res.Code, provider.calls)
	}

	res = httptest.NewRecorder()
	h.GetStatus(res, kycRequest(http.MethodGet, "/status", "kyc_sess_owner", "owner", nil, ""))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"sessionId":"kyc_sess_owner"`) {
		t.Fatalf("status: %d %s", res.Code, res.Body.String())
	}
}
