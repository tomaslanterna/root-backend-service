package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

const (
	unverifiedUser = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	verifiedUser   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	adminUser      = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	someAfter      = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

type accessFake map[string]domain.ViewerAccess

func (f accessFake) ViewerAccess(_ context.Context, id string) (domain.ViewerAccess, error) {
	if v, ok := f[id]; ok {
		return v, nil
	}
	return domain.ViewerAccess{}, domain.ErrUserNotFound
}

type afterServiceFake struct {
	ports.AfterService
	calls int
}

func (f *afterServiceFake) Create(_ context.Context, hostID string, fields domain.AfterFields) (*domain.AfterDetail, error) {
	f.calls++
	return &domain.AfterDetail{AfterSummary: domain.AfterSummary{ID: someAfter, Name: fields.Name, Genres: []string{}}}, nil
}

func (f *afterServiceFake) Update(context.Context, string, string, domain.AfterPatch) (*domain.AfterDetail, error) {
	f.calls++
	return &domain.AfterDetail{AfterSummary: domain.AfterSummary{ID: someAfter, Genres: []string{}}}, nil
}

func (f *afterServiceFake) AdminList(context.Context, string, string, int, int) ([]domain.AdminAfter, error) {
	f.calls++
	return []domain.AdminAfter{}, nil
}

func (f *afterServiceFake) AdminSetStatus(context.Context, string, string, string) error {
	f.calls++
	return nil
}

type submissionServiceFake struct {
	ports.EventSubmissionService
	calls int
}

func (f *submissionServiceFake) Create(context.Context, string, domain.EventSubmissionInput) (domain.EventSubmission, error) {
	f.calls++
	return domain.EventSubmission{ID: "s", Genres: []string{}}, nil
}

func (f *submissionServiceFake) AdminList(context.Context, string, string, int, int) ([]domain.AdminEventSubmission, error) {
	f.calls++
	return []domain.AdminEventSubmission{}, nil
}

func (f *submissionServiceFake) Publish(context.Context, string, string) (string, error) {
	f.calls++
	return "ev", nil
}

func (f *submissionServiceFake) Reject(context.Context, string, string, string) error {
	f.calls++
	return nil
}

func testJWT(t *testing.T, userID string) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix()}).
		SignedString([]byte("afters-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func imageBody(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("image", "x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0})
	writer.Close()
	return &buf, writer.FormDataContentType()
}

func newAftersTestRouter(afters *afterServiceFake, submissions *submissionServiceFake) http.Handler {
	return NewRouter(RouterConfig{
		AfterHandler:           NewAfterHandler(afters, nil),
		EventSubmissionHandler: NewEventSubmissionHandler(submissions, nil),
		ViewerAccess: accessFake{
			unverifiedUser: {IsKycVerified: false, Dob: "1990-01-01", Role: "USER"},
			verifiedUser:   {IsKycVerified: true, Dob: "1990-01-01", Role: "USER"},
			adminUser:      {IsKycVerified: true, Dob: "1990-01-01", Role: "ADMIN"},
		},
	})
}

func doRequest(t *testing.T, router http.Handler, method, path, user string, multipartBody bool) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if multipartBody {
		body, contentType := imageBody(t)
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", contentType)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(`{"name":"After","reason":"x"}`))
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+testJWT(t, user))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) APIError {
	t.Helper()
	var body APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an API error: %s", rec.Body.String())
	}
	return body
}

// Todos los endpoints [KYC] rechazan a un usuario sin verificar con 403 kyc_required, sin llegar al servicio.
func TestKycRequiredOnEveryKycEndpoint(t *testing.T) {
	t.Setenv("JWT_SECRET", "afters-test-secret")
	endpoints := []struct {
		method, path string
		multipart    bool
	}{
		{http.MethodPost, "/v1/afters/cover", true},
		{http.MethodPost, "/v1/afters", false},
		{http.MethodPatch, "/v1/afters/" + someAfter, false},
		{http.MethodPost, "/v1/event-submissions/flyer", true},
		{http.MethodPost, "/v1/event-submissions", false},
	}
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			afters, submissions := &afterServiceFake{}, &submissionServiceFake{}
			router := newAftersTestRouter(afters, submissions)

			rec := doRequest(t, router, ep.method, ep.path, unverifiedUser, ep.multipart)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("unverified: want 403, got %d %s", rec.Code, rec.Body.String())
			}
			if body := decodeAPIError(t, rec); body.Code != "kyc_required" || body.Status != 403 || body.Message != "Necesitás verificar tu identidad" {
				t.Fatalf("unverified: unexpected body %+v", body)
			}
			if afters.calls+submissions.calls != 0 {
				t.Fatal("the service must not be reached without identity verification")
			}
			if rec := doRequest(t, router, ep.method, ep.path, "", ep.multipart); rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous: want 401, got %d", rec.Code)
			}
			// Verificado: pasa el filtro (las subidas sin S3 responden 503; el resto llega al servicio).
			rec = doRequest(t, router, ep.method, ep.path, verifiedUser, ep.multipart)
			if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Fatalf("verified: must pass the gate, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminEndpointsRequireAdminRole(t *testing.T) {
	t.Setenv("JWT_SECRET", "afters-test-secret")
	for _, ep := range []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/afters?status=hidden"},
		{http.MethodPost, "/v1/admin/afters/" + someAfter + "/restore"},
		{http.MethodPost, "/v1/admin/afters/" + someAfter + "/remove"},
		{http.MethodGet, "/v1/admin/event-submissions?status=pending"},
		{http.MethodPost, "/v1/admin/event-submissions/" + someAfter + "/publish"},
		{http.MethodPost, "/v1/admin/event-submissions/" + someAfter + "/reject"},
	} {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			afters, submissions := &afterServiceFake{}, &submissionServiceFake{}
			router := newAftersTestRouter(afters, submissions)
			rec := doRequest(t, router, ep.method, ep.path, verifiedUser, false)
			if rec.Code != http.StatusForbidden || decodeAPIError(t, rec).Code != "forbidden" || afters.calls+submissions.calls != 0 {
				t.Fatalf("non admin: %d %s", rec.Code, rec.Body.String())
			}
			rec = doRequest(t, router, ep.method, ep.path, adminUser, false)
			if rec.Code >= 300 || afters.calls+submissions.calls != 1 {
				t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRequireKycFailsClosedWithoutRepository(t *testing.T) {
	handler := RequireKyc(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("must not pass without a repository")
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, verifiedUser))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestWriteAPIErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrKycRequired, 403, "kyc_required"},
		{domain.ErrUnderage, 403, "underage"},
		{domain.ErrNotAfterHost, 403, "not_host"},
		{domain.ErrAfterNotFound, 404, "not_found"},
		{domain.ErrAfterFull, 409, "after_full"},
		{domain.ErrAlreadyJoined, 409, "already_joined"},
		{domain.ErrAfterEnded, 409, "after_ended"},
		{domain.ErrInvalidEvent, 422, "invalid_event"},
		{domain.ErrTooManyActiveAfters, 429, "too_many_active"},
		{domain.ErrTooManyPendingSubmissions, 429, "too_many_pending"},
		{domain.ErrContentPolicyViolation, 422, "content_policy"},
		{&domain.ValidationError{Message: "name must have between 1 and 60 characters"}, 400, "invalid_request"},
		{errors.New("pq: connection refused"), 500, "internal_error"},
	} {
		rec := httptest.NewRecorder()
		writeAPIError(rec, tc.err)
		body := decodeAPIError(t, rec)
		if rec.Code != tc.status || body.Status != tc.status || body.Code != tc.code {
			t.Fatalf("%v: %d %+v", tc.err, rec.Code, body)
		}
		if tc.status == 500 && strings.Contains(body.Message, "pq") {
			t.Fatal("internal errors are not exposed")
		}
	}
}

// Los listados nunca llevan la dirección: AfterSummary no tiene ese campo.
func TestAfterSummaryJSONHasNoAddress(t *testing.T) {
	raw, err := json.Marshal(domain.AfterSummary{ID: someAfter, Genres: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "address") {
		t.Fatalf("summary must not have an address: %s", raw)
	}
}

func TestPatchFieldDistinguishesNullFromMissing(t *testing.T) {
	var body afterPatchBody
	if err := json.Unmarshal([]byte(`{"capacity":null,"price":500,"notice":"hola"}`), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Capacity.Set || body.Capacity.Value != nil || body.EndsAt.Set {
		t.Fatalf("capacity null must clear; endsAt missing must not: %+v", body)
	}
	if !body.Price.Set || body.Price.Value == nil || string(*body.Price.Value) != "500" || *body.Notice.Value != "hola" {
		t.Fatalf("price as number and notice: %+v", body)
	}
}
