package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"root-backend-service/internal/core/domain"
)

type ReviewReminderService interface {
	Checkout(context.Context, string, string) error
	OpenReminder(context.Context, string, string) (*domain.EventBadge, error)
}
type ReviewReminderHandler struct{ service ReviewReminderService }

func NewReviewReminderHandler(service ReviewReminderService) *ReviewReminderHandler {
	return &ReviewReminderHandler{service: service}
}
func (h *ReviewReminderHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(UserIDKey).(string)
	if user == "" {
		respondWithError(w, 401, "Iniciá sesión")
		return
	}
	if body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1)); err != nil || len(body) != 0 {
		respondWithError(w, 400, "No se aceptan datos de usuario")
		return
	}
	err := h.service.Checkout(r.Context(), user, chi.URLParam(r, "id"))
	if errors.Is(err, domain.ErrInvalidSurvey) {
		respondWithError(w, 400, "Evento inválido")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, 404, "No hay una asistencia por ubicación habilitada para finalizar")
		return
	}
	if err != nil {
		respondWithError(w, 500, "No pudimos confirmar la salida")
		return
	}
	w.WriteHeader(204)
}
func (h *ReviewReminderHandler) Open(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, _ := r.Context().Value(UserIDKey).(string)
	if user == "" {
		respondWithError(w, 401, "Iniciá sesión")
		return
	}
	var req struct {
		OpeningID string `json:"openingId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		respondWithError(w, 400, "Apertura inválida")
		return
	}
	badge, err := h.service.OpenReminder(r.Context(), user, req.OpeningID)
	if errors.Is(err, domain.ErrInvalidSurvey) {
		respondWithError(w, 400, "Apertura inválida")
		return
	}
	if err != nil {
		respondWithError(w, 500, "No pudimos consultar la reseña pendiente")
		return
	}
	respondWithJSON(w, 200, map[string]any{"badge": badge})
}
