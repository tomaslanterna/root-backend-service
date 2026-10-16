package handlers

import (
	"encoding/json"
	"errors"
	"io"
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
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, 401, "Iniciá sesión")
		return
	}

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
	GeneralRating      *int                  `json:"general_rating"`
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

	var req SubmitSurveyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if req.GeneralRating == nil || decoder.Decode(&struct{}{}) != io.EOF {
		respondWithError(w, 400, "Seleccioná una puntuación de 0 a 5")
		return
	}

	// Verificar que el evento haya terminado antes de permitir la encuesta
	event, err := h.eventService.GetEventByID(r.Context(), eventID, userID)
	if err == nil && event != nil {
		endTime := event.Date.Add(4 * time.Hour)
		if event.EndDate != nil && event.EndDate.After(event.Date) {
			endTime = *event.EndDate
		}
		if time.Now().Before(endTime) {
			respondWithError(w, http.StatusBadRequest, "La encuesta estará disponible una vez que la fiesta haya finalizado")
			return
		}
	}

	surveyID := uuid.NewString()

	surveyDomain := &domain.EventSurvey{
		ID:                 surveyID,
		UserID:             userID,
		EventID:            eventID,
		GeneralRating:      *req.GeneralRating,
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
	if req.ArtistRatings != nil {
		artistRatings = make([]domain.SurveyArtistRating, 0, len(req.ArtistRatings))
	}
	for _, ar := range req.ArtistRatings {
		artistRatings = append(artistRatings, domain.SurveyArtistRating{
			ID:       uuid.NewString(),
			SurveyID: surveyID,
			ArtistID: ar.ArtistID,
			Rating:   ar.Rating,
		})
	}

	err = h.surveyService.SubmitSurvey(r.Context(), surveyDomain, artistRatings)
	if err != nil {
		if errors.Is(err, domain.ErrSurveyConfirmed) {
			respondWithError(w, http.StatusConflict, "La reseña ya fue confirmada y no se puede modificar")
			return
		}
		if errors.Is(err, domain.ErrAttendanceRequired) {
			respondWithError(w, 403, "Necesitás asistencia confirmada para valorar esta fiesta")
			return
		}
		if errors.Is(err, domain.ErrInvalidSurvey) {
			respondWithError(w, http.StatusBadRequest, "Valoración inválida (0 a 5 estrellas; texto de hasta 1000 caracteres)")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "No pudimos guardar la valoración")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Valoración guardada"})
}
