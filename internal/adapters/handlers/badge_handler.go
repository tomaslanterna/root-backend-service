package handlers

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"root-backend-service/internal/core/domain"
	"strconv"
)

type BadgeService interface {
	List(context.Context, string, string, int, int) ([]domain.EventBadge, int, error)
	Get(context.Context, string, string) (*domain.EventBadge, error)
}
type BadgeHandler struct{ service BadgeService }

func NewBadgeHandler(s BadgeService) *BadgeHandler { return &BadgeHandler{service: s} }
func (h *BadgeHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	limit, offset := 12, 0
	for key, dest := range map[string]*int{"limit": &limit, "offset": &offset} {
		if value := r.URL.Query().Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || (key == "limit" && (n < 1 || n > 50)) {
				respondWithError(w, 400, "Paginación inválida")
				return
			}
			*dest = n
		}
	}
	viewer, _ := r.Context().Value(UserIDKey).(string)
	badges, total, err := h.service.List(r.Context(), chi.URLParam(r, "username"), viewer, limit, offset)
	if err != nil {
		respondWithError(w, 500, "No pudimos cargar las insignias")
		return
	}
	respondWithJSON(w, 200, map[string]any{"data": badges, "meta": map[string]any{"total": total, "limit": limit, "offset": offset, "hasMore": offset+len(badges) < total}})
}
func (h *BadgeHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, 401, "Iniciá sesión")
		return
	}
	eventID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(eventID); err != nil {
		respondWithError(w, 400, "Evento inválido")
		return
	}
	badge, err := h.service.Get(r.Context(), userID, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, 404, "Todavía no tenés asistencia confirmada a esta fiesta")
		return
	}
	if err != nil {
		respondWithError(w, 500, "No pudimos cargar la insignia")
		return
	}
	respondWithJSON(w, 200, badge)
}
