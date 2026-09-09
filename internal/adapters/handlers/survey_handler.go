package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/survey"
)

type SurveyHandler struct {
	surveyService *survey.SurveyService
	eventService  ports.EventService
}

func NewSurveyHandler(surveyService *survey.SurveyService, eventService ports.EventService) *SurveyHandler {
	return &SurveyHandler{
		surveyService: surveyService,
		eventService:  eventService,
	}
}

func (h *SurveyHandler) GetPendingSurveys(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(UserIDKey).(string)

	events, err := h.eventService.GetPendingSurveys(r.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, events)
}

type ArtistRatingPayload struct {
	ArtistID string `json:"artist_id"`
	Rating   int    `json:"rating"`
}

type SubmitSurveyRequest struct {
	GeneralRating      int                   `json:"general_rating"`
	OrganizationRating *int                  `json:"organization_rating"`
	VibeRating         *int                  `json:"vibe_rating"`
	SoundVisualRating  *int                  `json:"sound_visual_rating"`
	PricingRating      *int                  `json:"pricing_rating"`
	SpaceRating        *string               `json:"space_rating"`
	WouldReturn        *bool                 `json:"would_return"`
	Comment            *string               `json:"comment"`
	ArtistRatings      []ArtistRatingPayload `json:"artist_ratings"`
}

func (h *SurveyHandler) SubmitSurvey(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(UserIDKey).(string)
	eventID := chi.URLParam(r, "id")

	var req SubmitSurveyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	surveyID := uuid.NewString()

	surveyDomain := &domain.EventSurvey{
		ID:                 surveyID,
		UserID:             userID,
		EventID:            eventID,
		GeneralRating:      req.GeneralRating,
		OrganizationRating: req.OrganizationRating,
		VibeRating:         req.VibeRating,
		SoundVisualRating:  req.SoundVisualRating,
		PricingRating:      req.PricingRating,
		SpaceRating:        req.SpaceRating,
		WouldReturn:        req.WouldReturn,
		Comment:            req.Comment,
		CreatedAt:          time.Now(),
	}

	var artistRatings []domain.SurveyArtistRating
	for _, ar := range req.ArtistRatings {
		artistRatings = append(artistRatings, domain.SurveyArtistRating{
			ID:       uuid.NewString(),
			SurveyID: surveyID,
			ArtistID: ar.ArtistID,
			Rating:   ar.Rating,
		})
	}

	err := h.surveyService.SubmitSurvey(r.Context(), surveyDomain, artistRatings)
	if err != nil {
		if err.Error() == "user already submitted a survey for this event" {
			respondWithError(w, http.StatusConflict, err.Error())
			return
		}
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]string{"status": "success", "message": "Survey submitted"})
}
