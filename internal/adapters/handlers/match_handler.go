package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
	"github.com/go-chi/chi/v5"
)

type MatchHandler struct {
	service *services.MatchService
}

func NewMatchHandler(service *services.MatchService) *MatchHandler {
	return &MatchHandler{service: service}
}

func (h *MatchHandler) HandleSwipe(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "eventId")

	var req struct {
		Direction   string                 `json:"direction"`
		Preferences map[string]interface{} `json:"preferences"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	swipe := &domain.EventSwipe{
		UserID:      userID,
		EventID:     eventID,
		Direction:   req.Direction,
		Preferences: req.Preferences,
	}

	result, err := h.service.ProcessSwipe(r.Context(), swipe)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
