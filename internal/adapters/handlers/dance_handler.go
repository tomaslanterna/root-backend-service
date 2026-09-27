package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type DanceHandler struct {
	danceService ports.DanceService
}

func NewDanceHandler(svc ports.DanceService) *DanceHandler {
	return &DanceHandler{danceService: svc}
}

func (h *DanceHandler) SyncSteps(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	var req ports.SyncDanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	session, err := h.danceService.SyncSteps(r.Context(), userID, req)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, session)
}

func (h *DanceHandler) GetCrewLeaderboards(w http.ResponseWriter, r *http.Request) {
	squadID := chi.URLParam(r, "id")
	eventID := r.URL.Query().Get("eventId")

	var eventIDPtr *string
	if eventID != "" {
		eventIDPtr = &eventID
	}

	response, err := h.danceService.GetCrewLeaderboards(r.Context(), squadID, eventIDPtr)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get leaderboards")
		return
	}

	respondWithJSON(w, http.StatusOK, response)
}

func (h *DanceHandler) GetMyCrews(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}
	crews, err := h.danceService.GetUserCrews(r.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get crews")
		return
	}
	if crews == nil {
		crews = []map[string]interface{}{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": crews})
}

func (h *DanceHandler) GetMyDanceSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}
	sessions, err := h.danceService.GetUserDanceSessions(r.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get sessions")
		return
	}
	if sessions == nil {
		sessions = []map[string]interface{}{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": sessions})
}

func (h *DanceHandler) GetCrewByID(w http.ResponseWriter, r *http.Request) {
	squadID := chi.URLParam(r, "id")
	crew, err := h.danceService.GetCrewByID(r.Context(), squadID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Crew not found")
		return
	}
	respondWithJSON(w, http.StatusOK, crew)
}

func (h *DanceHandler) GetCrewEvents(w http.ResponseWriter, r *http.Request) {
	squadID := chi.URLParam(r, "id")
	events, err := h.danceService.GetCrewEvents(r.Context(), squadID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []map[string]interface{}{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": events})
}
