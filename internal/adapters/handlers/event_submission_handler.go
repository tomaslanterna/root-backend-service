package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

// EventSubmissionHandler expone "Mandanos tu evento" (contrato en AFTERS.md).
type EventSubmissionHandler struct {
	svc     ports.EventSubmissionService
	storage s3.S3Service
}

func NewEventSubmissionHandler(svc ports.EventSubmissionService, storage s3.S3Service) *EventSubmissionHandler {
	return &EventSubmissionHandler{svc: svc, storage: storage}
}

// lineupField acepta "A, B" o ["A", "B"] y lo guarda como texto.
type lineupField struct{ value *string }

func (l *lineupField) UnmarshalJSON(data []byte) error {
	var text *string
	if err := json.Unmarshal(data, &text); err == nil {
		l.value = text
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("lineup must be a string or a list of strings")
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		if item = strings.TrimSpace(item); item != "" {
			parts = append(parts, item)
		}
	}
	joined := strings.Join(parts, ", ")
	l.value = &joined
	return nil
}

func (h *EventSubmissionHandler) sign(r *http.Request, s *domain.EventSubmission) {
	s.FlyerURL = signStoredImage(r.Context(), h.storage, s.FlyerURL)
}

// GET /v1/events/similar?name=&date= — JWT opcional. {data: Event[] (máx 3)}
func (h *EventSubmissionHandler) Similar(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	date, err := parseOptionalDate(query.Get("date"), "date", false)
	if err != nil {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	events, err := h.svc.Similar(r.Context(), query.Get("name"), date)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	resolveEventImages(r.Context(), h.storage, events)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": events})
}

// POST /v1/event-submissions/flyer — [KYC]
func (h *EventSubmissionHandler) UploadFlyer(w http.ResponseWriter, r *http.Request) {
	uploadUserImage(w, r, h.storage, "event-submissions")
}

// POST /v1/event-submissions — [KYC]. 201 EventSubmission.
func (h *EventSubmissionHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name            string      `json:"name"`
		StartsAt        *time.Time  `json:"startsAt"`
		VenueName       string      `json:"venueName"`
		VenueAddress    string      `json:"venueAddress"`
		FlyerKey        string      `json:"flyerKey"`
		TicketURL       *string     `json:"ticketUrl"`
		Lineup          lineupField `json:"lineup"`
		Genres          []string    `json:"genres"`
		SubmitterRole   string      `json:"submitterRole"`
		Contact         string      `json:"contact"`
		RightsConfirmed bool        `json:"rightsConfirmed"`
	}
	if !decodeAPIBody(w, r, &body, false) {
		return
	}
	input := domain.EventSubmissionInput{
		Name: body.Name, VenueName: body.VenueName, VenueAddress: body.VenueAddress, FlyerKey: body.FlyerKey,
		TicketURL: body.TicketURL, Lineup: body.Lineup.value, Genres: body.Genres, SubmitterRole: body.SubmitterRole,
		Contact: body.Contact, RightsConfirmed: body.RightsConfirmed,
	}
	if body.StartsAt != nil {
		input.StartsAt = *body.StartsAt
	}
	created, err := h.svc.Create(r.Context(), userID, input)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.sign(r, &created)
	respondWithJSON(w, http.StatusCreated, created)
}

// GET /v1/users/me/event-submissions — los más nuevos primero.
func (h *EventSubmissionHandler) Mine(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ListMine(r.Context(), userID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	for i := range list {
		h.sign(r, &list[i])
	}
	w.Header().Set("Cache-Control", "private, no-store")
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

// GET /v1/admin/event-submissions?status=pending — ADMIN.
func (h *EventSubmissionHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.AdminList(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")), intQuery(r, "limit"), intQuery(r, "offset"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	for i := range list {
		h.sign(r, &list[i].EventSubmission)
		list[i].Submitter.AvatarURL = signStoredImage(r.Context(), h.storage, list[i].Submitter.AvatarURL)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

// POST /v1/admin/event-submissions/{id}/publish — ADMIN. {eventId}
func (h *EventSubmissionHandler) Publish(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	eventID, err := h.svc.Publish(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]string{"eventId": eventID})
}

// POST /v1/admin/event-submissions/{id}/reject {reason} — ADMIN. 204.
func (h *EventSubmissionHandler) Reject(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeAPIBody(w, r, &body, false) {
		return
	}
	if err := h.svc.Reject(r.Context(), userID, chi.URLParam(r, "id"), body.Reason); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
