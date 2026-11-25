package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

// TempCrewHandler expone las crews temporales (contrato en API_Specs.md del front). La identidad sale solo del JWT.
type TempCrewHandler struct {
	svc ports.TempCrewService
	s3  s3.S3Service
}

func NewTempCrewHandler(svc ports.TempCrewService, s3Services ...s3.S3Service) *TempCrewHandler {
	h := &TempCrewHandler{svc: svc}
	if len(s3Services) > 0 {
		h.s3 = s3Services[0]
	}
	return h
}

// tempCrewError agrega un `code` estable al formato de error habitual ({status, message}).
type tempCrewError struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func writeTempCrewError(w http.ResponseWriter, err error) {
	var validation *domain.ValidationError
	status, code, message := http.StatusInternalServerError, "internal_error", "Internal error"
	switch {
	case errors.As(err, &validation):
		status, code, message = http.StatusBadRequest, "invalid_request", validation.Message
	case errors.Is(err, domain.ErrInvalidID):
		status, code, message = http.StatusBadRequest, "invalid_id", "Invalid id"
	case errors.Is(err, domain.ErrTempCrewNotFound):
		status, code, message = http.StatusNotFound, "crew_not_found", "Crew not found"
	case errors.Is(err, domain.ErrEventNotFound):
		status, code, message = http.StatusNotFound, "event_not_found", "Event not found"
	case errors.Is(err, domain.ErrRequestNotFound):
		status, code, message = http.StatusNotFound, "request_not_found", "Join request not found"
	case errors.Is(err, domain.ErrNotTempCrewHost):
		status, code, message = http.StatusForbidden, "forbidden", "Solo quien creó la crew puede hacer esto"
	case errors.Is(err, domain.ErrEventEnded):
		status, code, message = http.StatusConflict, "event_ended", "El evento ya terminó"
	case errors.Is(err, domain.ErrAlreadyInEventCrew):
		status, code, message = http.StatusConflict, "already_in_event_crew", "Ya estás en una crew temporal de este evento"
	case errors.Is(err, domain.ErrAlreadyMember):
		status, code, message = http.StatusConflict, "already_member", "Ya sos parte de esta crew"
	case errors.Is(err, domain.ErrNotAdmitting):
		status, code, message = http.StatusConflict, "not_admitting", "La crew no está admitiendo gente"
	case errors.Is(err, domain.ErrCrewFull):
		status, code, message = http.StatusConflict, "crew_full", "La crew está llena"
	case errors.Is(err, domain.ErrRequestNotPending):
		status, code, message = http.StatusConflict, "request_not_pending", "La solicitud ya fue resuelta"
	case errors.Is(err, domain.ErrHostCannotLeave):
		status, code, message = http.StatusConflict, "host_cannot_leave", "Quien creó la crew no puede salir"
	case errors.Is(err, domain.ErrTooManyRequests):
		status, code, message = http.StatusTooManyRequests, "too_many_requests", "Demasiadas solicitudes, probá más tarde"
	default:
		log.Printf("temporary crew: %v", err)
	}
	respondWithJSON(w, status, tempCrewError{Status: "error", Message: message, Code: code})
}

func authenticatedUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return "", false
	}
	return userID, true
}

// decodeStrict rechaza campos desconocidos (por ejemplo `userId`) y cuerpos enormes. Con allowEmpty, el body puede faltar.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst interface{}, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		writeTempCrewError(w, &domain.ValidationError{Message: "Invalid payload"})
		return false
	}
	return true
}

func intQuery(r *http.Request, name string) int {
	value, _ := strconv.Atoi(r.URL.Query().Get(name))
	return value
}

// signBanner convierte las claves S3 del flyer en URLs firmadas, igual que el resto de las crews.
func (h *TempCrewHandler) signBanner(ctx context.Context, ref *domain.TempCrewEventRef) {
	signBannerRef(ctx, h.s3, ref)
}

func signBannerRef(ctx context.Context, s3Service s3.S3Service, ref *domain.TempCrewEventRef) {
	if s3Service == nil || ref == nil || ref.CinematicBannerURL == nil {
		return
	}
	raw := strings.TrimSpace(*ref.CinematicBannerURL)
	if raw == "" || strings.HasPrefix(raw, "http") {
		return
	}
	if url, err := s3Service.GenerateViewUrl(ctx, raw, 7*24*time.Hour); err == nil {
		ref.CinematicBannerURL = &url
	}
}

func (h *TempCrewHandler) signAll(ctx context.Context, list []domain.TempCrewSummary) {
	for i := range list {
		h.signBanner(ctx, &list[i].Event)
	}
}

// GET /v1/events/{id}/temp-crews — JWT opcional.
func (h *TempCrewHandler) ListForEvent(w http.ResponseWriter, r *http.Request) {
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	page, err := h.svc.ListForEvent(r.Context(), chi.URLParam(r, "id"), viewerID, intQuery(r, "limit"), intQuery(r, "offset"))
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	h.signAll(r.Context(), page.Data)
	respondWithJSON(w, http.StatusOK, page)
}

// POST /v1/events/{id}/temp-crews
func (h *TempCrewHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
		Plan     string `json:"plan"`
	}
	if !decodeStrict(w, r, &body, false) {
		return
	}
	crew, err := h.svc.Create(r.Context(), userID, chi.URLParam(r, "id"), domain.CreateTempCrewInput{Name: body.Name, Capacity: body.Capacity, Plan: body.Plan})
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	h.signBanner(r.Context(), &crew.Event)
	respondWithJSON(w, http.StatusCreated, map[string]interface{}{"crew": crew})
}

// GET /v1/users/me/temp-crews
func (h *TempCrewHandler) Mine(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ListMine(r.Context(), userID)
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	h.signAll(r.Context(), list)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

// GET /v1/temp-crews/discover
func (h *TempCrewHandler) Discover(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Discover(r.Context(), userID, intQuery(r, "limit"))
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	h.signAll(r.Context(), list)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

// POST /v1/crews/{id}/join-requests — body opcional {"message"}.
func (h *TempCrewHandler) RequestJoin(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if !decodeStrict(w, r, &body, true) {
		return
	}
	req, created, err := h.svc.RequestJoin(r.Context(), chi.URLParam(r, "id"), userID, body.Message)
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	respondWithJSON(w, status, map[string]interface{}{"request": req})
}

// DELETE /v1/crews/{id}/join-requests/me — idempotente.
func (h *TempCrewHandler) CancelMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	if err := h.svc.CancelMyRequest(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeTempCrewError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /v1/crews/{id}/join-requests?status=pending|rejected — solo el host.
func (h *TempCrewHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ListRequests(r.Context(), chi.URLParam(r, "id"), userID, r.URL.Query().Get("status"), intQuery(r, "limit"), intQuery(r, "offset"))
	if err != nil {
		writeTempCrewError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

func (h *TempCrewHandler) decide(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r)
		if !ok {
			return
		}
		req, err := h.svc.Decide(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "requestId"), userID, accept)
		if err != nil {
			writeTempCrewError(w, err)
			return
		}
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"request": req})
	}
}

// POST /v1/crews/{id}/join-requests/{requestId}/accept
func (h *TempCrewHandler) Accept(w http.ResponseWriter, r *http.Request) { h.decide(true)(w, r) }

// POST /v1/crews/{id}/join-requests/{requestId}/reject
func (h *TempCrewHandler) Reject(w http.ResponseWriter, r *http.Request) { h.decide(false)(w, r) }
