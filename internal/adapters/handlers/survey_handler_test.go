package handlers

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/services/survey"
	"strings"
	"testing"
)

type surveyHTTPRepo struct {
	record *domain.EventSurvey
	err    error
}

func (s *surveyHTTPRepo) CreateSurvey(_ context.Context, r *domain.EventSurvey, _ []domain.SurveyArtistRating) error {
	s.record = r
	return s.err
}
func TestSurveyHTTPValidationAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		body, user string
		repoError  error
		status     int
	}{
		{`{"general_rating":0}`, uuid.NewString(), nil, 200},
		{`{"general_rating":5}`, uuid.NewString(), domain.ErrAttendanceRequired, 403},
		{`{"general_rating":5}`, uuid.NewString(), domain.ErrSurveyConfirmed, 409},
		{`{"general_rating":5}`, "", nil, 401},
		{`{}`, uuid.NewString(), nil, 400},
		{`{"general_rating":null}`, uuid.NewString(), nil, 400},
		{`{"general_rating":6}`, uuid.NewString(), nil, 400},
		{`{"general_rating":-1}`, uuid.NewString(), nil, 400},
		{`{"general_rating":0,"userId":"fake"}`, uuid.NewString(), nil, 400},
		{`{"general_rating":0} {}`, uuid.NewString(), nil, 400},
	} {
		r := &surveyHTTPRepo{err: tc.repoError}
		h := NewSurveyHandler(survey.NewSurveyService(r), nil)
		request := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("id", uuid.NewString())
		request = request.WithContext(context.WithValue(context.WithValue(request.Context(), chi.RouteCtxKey, ctx), UserIDKey, tc.user))
		w := httptest.NewRecorder()
		h.SubmitSurvey(w, request)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d: %s", tc.body, w.Code, tc.status, w.Body.String())
		}
		if w.Code == 200 && (r.record.UserID != tc.user || r.record.GeneralRating != 0) {
			t.Fatal("did not preserve JWT subject / zero score")
		}
	}
}
