package handlers

import (
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
	"strings"
	"testing"
	"time"
)

type pushRepositoryFake struct {
	user, id, token string
	err             error
}

func (p *pushRepositoryFake) RegisterDevice(_ context.Context, user, id, token string) error {
	p.user, p.id, p.token = user, id, token
	return p.err
}
func (p *pushRepositoryFake) RemoveDevice(_ context.Context, user, id string) error {
	p.user, p.id = user, id
	return p.err
}
func (*pushRepositoryFake) ClaimJobs(context.Context) ([]domain.PushJob, error)   { return nil, nil }
func (*pushRepositoryFake) CanSend(context.Context, domain.PushJob) (bool, error) { return false, nil }
func (*pushRepositoryFake) FinishJob(context.Context, domain.PushJob, bool, time.Duration) error {
	return nil
}
func (*pushRepositoryFake) ExpireToken(context.Context, domain.PushJob) error { return nil }

type pushSenderFake struct{}

func (pushSenderFake) Send(context.Context, domain.PushJob) error { return nil }

func TestPushRegistrationHTTP(t *testing.T) {
	id := "48a09314-cedb-410b-a243-e45e688fb47e"
	valid := `{"platform":"android","token":"abcdefghijklmnopqrstuvwxyz"}`
	for _, test := range []struct {
		name, user, id, body string
		disabled             bool
		repoErr              error
		status               int
	}{
		{"valid", "jwt-user", id, valid, false, nil, 204},
		{"unauthenticated", "", id, valid, false, nil, 401},
		{"arbitrary user rejected", "jwt-user", id, `{"platform":"android","token":"abcdefghijklmnopqrstuvwxyz","userId":"attacker"}`, false, nil, 400},
		{"ios APNs rejected", "jwt-user", id, `{"platform":"ios","token":"abcdefghijklmnopqrstuvwxyz"}`, false, nil, 400},
		{"short token", "jwt-user", id, `{"platform":"android","token":"short"}`, false, nil, 400},
		{"invalid id", "jwt-user", "invalid", valid, false, nil, 400},
		{"extra JSON", "jwt-user", id, valid + `{}`, false, nil, 400},
		{"disabled", "jwt-user", id, valid, true, nil, 503},
		{"repository failure", "jwt-user", id, valid, false, errors.New("db"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &pushRepositoryFake{err: test.repoErr}
			var sender *services.PushService
			if test.disabled {
				sender = services.NewPushService(repo, nil)
			} else {
				sender = services.NewPushService(repo, pushSenderFake{})
			}
			h := NewPushHandler(sender)
			req := requestWithRouteAndUser(httptest.NewRequest("PUT", "/", strings.NewReader(test.body)), test.id, test.user)
			res := httptest.NewRecorder()
			h.Register(res, req)
			if res.Code != test.status {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			if test.status == 204 && (repo.user != test.user || repo.token != "abcdefghijklmnopqrstuvwxyz") {
				t.Fatal("must use JWT user")
			}
		})
	}
}

func TestPushRoutesRequireJWT(t *testing.T) {
	h := NewPushHandler(services.NewPushService(&pushRepositoryFake{}, pushSenderFake{}))
	r := chi.NewRouter()
	r.With(AuthMiddleware).Get("/push/status", h.Status)
	r.With(AuthMiddleware).Put("/push/devices/{id}", h.Register)
	r.With(AuthMiddleware).Delete("/push/devices/{id}", h.Remove)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		path := "/push/devices/48a09314-cedb-410b-a243-e45e688fb47e"
		if method == "GET" {
			path = "/push/status"
		}
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(method, path, nil))
		if res.Code != 401 {
			t.Fatalf("%s: %d", method, res.Code)
		}
	}
}
