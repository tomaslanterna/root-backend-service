package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/ports"
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

	log.Printf("Registrando %d pasos para el usuario %s en el evento %s", req.StepsCount, userID, req.EventID)

	respondWithJSON(w, http.StatusOK, session)
}

func (h *DanceHandler) CreatePermanentCrew(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	var body struct {
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		MemberIDs []string `json:"member_ids"`
		MemberIds []string `json:"memberIds"`
		EventID   *string  `json:"event_id"`
		EventId   *string  `json:"eventId"`
		EventIDs  []string `json:"event_ids"`
		EventIds  []string `json:"eventIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	memberIDs := append(body.MemberIDs, body.MemberIds...)
	eventIDs := append(body.EventIDs, body.EventIds...)
	eventID := body.EventID
	if eventID == nil {
		eventID = body.EventId
	}

	crew, err := h.danceService.CreatePermanentCrew(r.Context(), userID, ports.CreateCrewRequest{
		Name:      body.Name,
		Type:      body.Type,
		MemberIDs: memberIDs,
		EventID:   eventID,
		EventIDs:  eventIDs,
	})
	if err != nil {
		log.Printf("Error creating permanent crew for user %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "Failed to create crew")
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"crew":    crew,
	})
}

func (h *DanceHandler) JoinPermanentCrew(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	codeOrID := strings.TrimSpace(chi.URLParam(r, "inviteCode"))
	if codeOrID == "" {
		codeOrID = strings.TrimSpace(chi.URLParam(r, "id"))
	}
	if codeOrID == "" {
		var body struct {
			InviteCode string `json:"inviteCode"`
			CrewID     string `json:"crewId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.InviteCode != "" {
			codeOrID = body.InviteCode
		} else {
			codeOrID = body.CrewID
		}
	}

	crew, err := h.danceService.JoinPermanentCrew(r.Context(), userID, codeOrID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Crew not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Failed to join crew")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"crewId":  crew["id"],
		"crew":    crew,
		"message": "Successfully joined the crew",
	})
}

func (h *DanceHandler) AddCrewEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	squadID := chi.URLParam(r, "id")
	var body struct {
		EventID string `json:"eventId"`
		EventId string `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid payload")
		return
	}
	eventID := strings.TrimSpace(body.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(body.EventId)
	}
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "eventId is required")
		return
	}

	if err := h.danceService.AddCrewEvent(r.Context(), squadID, eventID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Crew or event not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Failed to associate event with crew")
		return
	}

	events, err := h.danceService.GetCrewEvents(r.Context(), squadID)
	if err != nil {
		events = []map[string]interface{}{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": events})
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
	scope := r.URL.Query().Get("scope")
	if scope == "all" {
		crews, err := h.danceService.GetAllCrews(r.Context())
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Failed to get crews")
			return
		}
		if crews == nil {
			crews = []map[string]interface{}{}
		}
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": crews})
		return
	}

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
