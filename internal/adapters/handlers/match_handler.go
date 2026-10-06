package handlers

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
)

type MatchHandler struct {
	service *services.MatchService
}

func NewMatchHandler(service *services.MatchService) *MatchHandler {
	return &MatchHandler{service: service}
}

func (h *MatchHandler) HandleSwipe(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "eventId")
	if _, err := uuid.Parse(eventID); err != nil {
		respondWithError(w, 400, "Invalid event ID")
		return
	}

	var req struct {
		Direction   string                 `json:"direction"`
		Preferences map[string]interface{} `json:"preferences"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
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
		respondWithMatchError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *MatchHandler) GetMatches(w http.ResponseWriter, r *http.Request) {
	squads, err := h.service.GetUserSquads(r.Context(), r.Context().Value(UserIDKey).(string))
	if err != nil {
		respondWithMatchError(w, err)
		return
	}
	respondWithJSON(w, 200, map[string]interface{}{"data": squads})
}

// POST is explicit because historic squads may not yet have a persisted chat.
func (h *MatchHandler) EnsureSquadChat(w http.ResponseWriter, r *http.Request) {
	chatID, err := h.service.EnsureSquadChat(r.Context(), chi.URLParam(r, "id"), r.Context().Value(UserIDKey).(string))
	if err != nil {
		respondWithMatchError(w, err)
		return
	}
	respondWithJSON(w, 200, map[string]string{"chatId": chatID})
}

func respondWithMatchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidSwipe):
		respondWithError(w, 400, "Invalid swipe or squad ID")
	case errors.Is(err, domain.ErrSquadNotFound):
		respondWithError(w, 404, "Event or squad not found")
	case errors.Is(err, domain.ErrSquadForbidden):
		respondWithError(w, 403, domain.ErrSquadForbidden.Error())
	case errors.Is(err, domain.ErrSquadChatConflict):
		respondWithError(w, 409, domain.ErrSquadChatConflict.Error())
	default:
		respondWithError(w, 500, "Could not load or create squad chat")
	}
}
