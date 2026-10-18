package handlers

import (
	"context"
	"database/sql"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	core "root-backend-service/internal/core/services"
	"strings"
	"testing"
)

type reminderFake struct {
	user, event, opening string
	err                  error
	badge                *domain.EventBadge
}

func (f *reminderFake) Checkout(_ context.Context, user, event string) error {
	f.user, f.event = user, event
	return f.err
}
func (f *reminderFake) OpenReminder(_ context.Context, user, opening string) (*domain.EventBadge, error) {
	f.user, f.opening = user, opening
	return f.badge, f.err
}
func TestReviewReminderHTTP(t *testing.T) {
	user, event, opening := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name, method, path, body string
		auth                     bool
		err                      error
		want                     int
	}{
		{"open without session", "POST", "/open", `{}`, false, nil, 401},
		{"invalid opening", "POST", "/open", `{"openingId":"bad"}`, true, nil, 400},
		{"no forged user", "POST", "/open", `{"openingId":"` + opening + `","userId":"other"}`, true, nil, 400},
		{"trailing JSON", "POST", "/open", `{"openingId":"` + opening + `"} {}`, true, nil, 400},
		{"open", "POST", "/open", `{"openingId":"` + opening + `"}`, true, nil, 200},
		{"open failure", "POST", "/open", `{"openingId":"` + opening + `"}`, true, sql.ErrConnDone, 500},
		{"checkout without session", "POST", "/" + event, "", false, nil, 401},
		{"checkout forged user", "POST", "/" + event, `{"userId":"other"}`, true, nil, 400},
		{"checkout", "POST", "/" + event, "", true, nil, 204},
		{"checkout invalid", "POST", "/bad", "", true, nil, 400},
		{"checkout foreign or manual", "POST", "/" + event, "", true, sql.ErrNoRows, 404},
		{"checkout failure", "POST", "/" + event, "", true, sql.ErrConnDone, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &reminderFake{err: tc.err}
			handler := NewReviewReminderHandler(core.NewReviewReminderService(fake))
			r := chi.NewRouter()
			r.Post("/open", handler.Open)
			r.Post("/{id}", handler.Checkout)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.auth {
				req = req.WithContext(context.WithValue(req.Context(), UserIDKey, user))
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if (tc.want == 200 || tc.want == 204) && fake.user != user {
				t.Fatal("did not use authenticated identity")
			}
		})
	}
}
