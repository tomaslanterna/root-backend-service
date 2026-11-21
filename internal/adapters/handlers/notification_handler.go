package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type NotificationHandler struct{ service *services.NotificationService }

func NewNotificationHandler(service *services.NotificationService) *NotificationHandler {
	return &NotificationHandler{service: service}
}

func notificationUser(r *http.Request) string {
	userID, _ := r.Context().Value(UserIDKey).(string)
	return userID
}

func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	page, err := h.service.List(r.Context(), notificationUser(r), limit, offset)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron cargar las notificaciones")
		return
	}
	respondWithJSON(w, http.StatusOK, page)
}

func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		respondWithError(w, http.StatusBadRequest, "Notificación inválida")
		return
	}
	if err := h.service.MarkRead(r.Context(), notificationUser(r), id); err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudo actualizar la notificación")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	if err := h.service.MarkAllRead(r.Context(), notificationUser(r)); err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron actualizar las notificaciones")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := h.service.GetPreferences(r.Context(), notificationUser(r))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron cargar las preferencias")
		return
	}
	respondWithJSON(w, http.StatusOK, prefs)
}

func (h *NotificationHandler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	var prefs domain.NotificationPreferences
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prefs); err != nil {
		respondWithError(w, http.StatusBadRequest, "Preferencias inválidas")
		return
	}
	if err := h.service.UpdatePreferences(r.Context(), notificationUser(r), prefs); err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron guardar las preferencias")
		return
	}
	respondWithJSON(w, http.StatusOK, prefs)
}
