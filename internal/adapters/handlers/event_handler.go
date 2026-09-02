package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

const (
	defaultEventPageSize   = 12
	defaultRelatedPageSize = 20
	maxPageSize            = 50
	maxCommentLength       = 1000
)

type EventHandler struct {
	eventService ports.EventService
}

func NewEventHandler(eventService ports.EventService) *EventHandler {
	return &EventHandler{eventService: eventService}
}

func parseOptionalBool(value, name string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, errors.New(name + " debe ser true o false")
	}
	return &parsed, nil
}

func parseOptionalFloat(value, name string) (*float64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return nil, errors.New(name + " debe ser un número mayor o igual a cero")
	}
	return &parsed, nil
}

func parseOptionalDate(value, name string, endOfDay bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, errors.New(name + " debe usar RFC3339 o YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return &parsed, nil
}

func parsePagination(r *http.Request, defaultLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	var err error

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return 0, 0, errors.New("limit debe ser un entero mayor a cero")
		}
		if limit > maxPageSize {
			limit = maxPageSize
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset debe ser un entero mayor o igual a cero")
		}
	}
	return limit, offset, nil
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, offset, err := parsePagination(r, defaultEventPageSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	filter := domain.EventFilter{
		Genre:    strings.TrimSpace(query.Get("genre")),
		Location: strings.TrimSpace(query.Get("location")),
		Query:    strings.TrimSpace(query.Get("query")),
		Limit:    limit,
		Offset:   offset,
	}
	if filter.FeaturedOnly, err = parseOptionalBool(query.Get("featured"), "featured"); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.IsFree, err = parseOptionalBool(query.Get("isFree"), "isFree"); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.MinPrice, err = parseOptionalFloat(query.Get("minPrice"), "minPrice"); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.MaxPrice, err = parseOptionalFloat(query.Get("maxPrice"), "maxPrice"); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.StartDate, err = parseOptionalDate(query.Get("startDate"), "startDate", false); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.EndDate, err = parseOptionalDate(query.Get("endDate"), "endDate", true); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filter.MinPrice != nil && filter.MaxPrice != nil && *filter.MinPrice > *filter.MaxPrice {
		respondWithError(w, http.StatusBadRequest, "minPrice no puede ser mayor que maxPrice")
		return
	}
	if filter.StartDate != nil && filter.EndDate != nil && filter.StartDate.After(*filter.EndDate) {
		respondWithError(w, http.StatusBadRequest, "startDate no puede ser posterior a endDate")
		return
	}

	currentUserID, _ := r.Context().Value(UserIDKey).(string)
	events, total, err := h.eventService.GetEvents(r.Context(), filter, currentUserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo eventos")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(events) < total,
		},
	})
}

func (h *EventHandler) GetFeaturedEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.eventService.GetFeaturedEvents(r.Context(), strings.TrimSpace(r.URL.Query().Get("country")))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo eventos destacados")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
		"meta": map[string]interface{}{"total": len(events)},
	})
}

func (h *EventHandler) GetEventByID(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "ID de evento requerido")
		return
	}
	currentUserID, _ := r.Context().Value(UserIDKey).(string)
	event, err := h.eventService.GetEventByID(r.Context(), eventID, currentUserID)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo el evento")
		return
	}
	respondWithJSON(w, http.StatusOK, event)
}

func (h *EventHandler) RSVPEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}

	var request struct {
		Status string `json:"status"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cuerpo de solicitud inválido")
		return
	}
	if request.Status != "going" && request.Status != "not_going" {
		respondWithError(w, http.StatusBadRequest, "status debe ser going o not_going")
		return
	}

	goingCount, notGoingCount, userRSVP, err := h.eventService.RSVPEvent(r.Context(), userID, eventID, request.Status)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento o usuario no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error procesando RSVP")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "goingCount": goingCount,
		"notGoingCount": notGoingCount, "userRsvp": userRSVP,
	})
}

func (h *EventHandler) DeleteEventRSVP(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}

	goingCount, notGoingCount, err := h.eventService.ClearEventRSVP(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento o usuario no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error eliminando RSVP")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "goingCount": goingCount,
		"notGoingCount": notGoingCount, "userRsvp": nil,
	})
}

func (h *EventHandler) GetFollowedGoingAttendees(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	limit, offset, err := parsePagination(r, defaultRelatedPageSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	attendees, total, err := h.eventService.GetFollowedGoingAttendees(r.Context(), chi.URLParam(r, "id"), userID, limit, offset)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo asistentes seguidos")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": attendees,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(attendees) < total,
		},
	})
}

func (h *EventHandler) GetEventComments(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r, defaultRelatedPageSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	comments, total, err := h.eventService.GetEventComments(r.Context(), chi.URLParam(r, "id"), limit, offset)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo comentarios")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": comments,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(comments) < total,
		},
	})
}

func (h *EventHandler) CreateEventComment(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	var request struct {
		Content string `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cuerpo de solicitud inválido")
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	if request.Content == "" {
		respondWithError(w, http.StatusBadRequest, "Contenido de comentario requerido")
		return
	}
	if !utf8.ValidString(request.Content) || utf8.RuneCountInString(request.Content) > maxCommentLength {
		respondWithError(w, http.StatusBadRequest, "El comentario no puede superar 1000 caracteres")
		return
	}

	comment, err := h.eventService.CreateEventComment(r.Context(), chi.URLParam(r, "id"), userID, request.Content)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creando comentario")
		return
	}
	respondWithJSON(w, http.StatusCreated, comment)
}

func (h *EventHandler) GetEventTickets(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": []map[string]interface{}{{
			"id": "t1", "eventId": eventID, "sellerId": "2",
			"price": 45000, "status": "AVAILABLE",
		}},
	})
}
