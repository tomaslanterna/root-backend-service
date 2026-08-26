package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/ports"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type EventHandler struct {
	eventService ports.EventService
}

func NewEventHandler(eventService ports.EventService) *EventHandler {
	return &EventHandler{
		eventService: eventService,
	}
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	featuredStr := r.URL.Query().Get("featured")
	country := r.URL.Query().Get("country")

	var featuredOnly *bool
	if featuredStr != "" {
		val, err := strconv.ParseBool(featuredStr)
		if err == nil {
			featuredOnly = &val
		}
	}

	events, err := h.eventService.GetEvents(r.Context(), featuredOnly, country)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo eventos: "+err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
		"meta": map[string]interface{}{
			"total": len(events),
		},
	})
}

func (h *EventHandler) GetFeaturedEvents(w http.ResponseWriter, r *http.Request) {
	country := r.URL.Query().Get("country")

	events, err := h.eventService.GetFeaturedEvents(r.Context(), country)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo eventos destacados: "+err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
		"meta": map[string]interface{}{
			"total": len(events),
		},
	})
}

func (h *EventHandler) GetEventByID(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "ID de evento requerido")
		return
	}

	event, err := h.eventService.GetEventByID(r.Context(), eventID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}

	respondWithJSON(w, http.StatusOK, event)
}

func (h *EventHandler) RSVPEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "ID de evento requerido")
		return
	}

	var req struct {
		UserID string `json:"userId"`
		Status string `json:"status"` // 'going' or 'not_going'
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cuerpo de solicitud inválido")
		return
	}

	if req.Status == "" {
		req.Status = "going"
	}

	userID := req.UserID
	if ctxUserID, ok := r.Context().Value(UserIDKey).(string); ok && ctxUserID != "" {
		userID = ctxUserID
	}
	if userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}

	goingCount, notGoingCount, err := h.eventService.RSVPEvent(r.Context(), userID, eventID, req.Status)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error procesando RSVP: "+err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"goingCount":    goingCount,
		"notGoingCount": notGoingCount,
	})
}

func (h *EventHandler) GetEventTickets(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":       "t1",
				"eventId":  eventID,
				"sellerId": "2",
				"price":    45000,
				"status":   "AVAILABLE",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
