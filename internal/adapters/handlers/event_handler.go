package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

const (
	defaultEventPageSize   = 10
	defaultRelatedPageSize = 20
	maxPageSize            = 50
	maxCommentLength       = 1000
)

type EventHandler struct {
	eventService ports.EventService
	s3Service    s3.S3Service
	userRepo     ports.UserRepository
	tempCrews    ports.TempCrewService
	rrpps        ports.EventRRPPService
}

// SetTempCrewService habilita el resumen de crews temporales en el detalle del evento (`tempCrews`).
func (h *EventHandler) SetTempCrewService(svc ports.TempCrewService) { h.tempCrews = svc }

// SetEventRRPPService habilita la carga de los RRPPs oficiales en el detalle del evento (`rrpps`).
func (h *EventHandler) SetEventRRPPService(svc ports.EventRRPPService) { h.rrpps = svc }

// eventDetailResponse es el evento más el resumen de crews temporales y los RRPPs oficiales. Solo lo usa el
// detalle: el listado y los destacados siguen devolviendo domain.Event sin `tempCrews` ni `rrpps`.
type eventDetailResponse struct {
	*domain.Event
	TempCrews *domain.TempCrewsEventSummary `json:"tempCrews,omitempty"`
	// RRPPs siempre está presente en el detalle: [] si el evento no tiene.
	RRPPs []domain.EventRRPP `json:"rrpps"`
}

func NewEventHandler(eventService ports.EventService, s3Service s3.S3Service, userRepo ports.UserRepository) *EventHandler {
	return &EventHandler{eventService: eventService, s3Service: s3Service, userRepo: userRepo}
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

	h.enrichEventImages(r.Context(), events)

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
	// Con sesión: primero los que coinciden con sus intereses musicales. Si no se pueden leer, orden normal.
	if userID, _ := r.Context().Value(UserIDKey).(string); userID != "" && h.userRepo != nil {
		if interests, _, err := h.userRepo.GetInterests(r.Context(), userID); err == nil {
			events = domain.RankByInterests(events, interests)
		}
	}

	h.enrichEventImages(r.Context(), events)

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

	// Con sesión, la respuesta incluye datos de quien consulta (viewerCrewId, viewerRequestStatus…): nada de caché
	// compartida. Sin sesión es igual para todos.
	w.Header().Add("Vary", "Authorization")
	if currentUserID != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}

	// Las crews temporales se piden en paralelo, en una consulta aparte (un JOIN repetiría la fila del evento por
	// cada crew). Si fallan, el evento se devuelve igual sin `tempCrews` y el front usa /temp-crews.
	crewsCtx, cancelCrews := context.WithCancel(r.Context())
	defer cancelCrews()
	var crewsDone chan *domain.TempCrewsEventSummary
	if h.tempCrews != nil {
		crewsDone = make(chan *domain.TempCrewsEventSummary, 1)
		go func() {
			summary, err := h.tempCrews.EventSummary(crewsCtx, eventID, currentUserID)
			if err != nil {
				if crewsCtx.Err() == nil {
					log.Printf("event %s: temporary crews summary unavailable: %v", eventID, err)
				}
				summary = nil
			}
			crewsDone <- summary
		}()
	}
	waitCrews := func() *domain.TempCrewsEventSummary {
		if crewsDone == nil {
			return nil
		}
		return <-crewsDone
	}

	event, err := h.eventService.GetEventByID(r.Context(), eventID, currentUserID)
	if errors.Is(err, sql.ErrNoRows) {
		cancelCrews()
		waitCrews()
		respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		return
	}
	if err != nil {
		cancelCrews()
		waitCrews()
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo el evento")
		return
	}
	// RRPPs: consulta aparte (como el lineup), con el id canónico que devolvió el repositorio.
	rrpps := []domain.EventRRPP{}
	if h.rrpps != nil {
		loaded, err := h.rrpps.ListForEvent(r.Context(), event.ID)
		if err != nil {
			cancelCrews()
			waitCrews()
			log.Printf("event %s: loading rrpps: %v", event.ID, err)
			respondWithError(w, http.StatusInternalServerError, "Error obteniendo el evento")
			return
		}
		if loaded != nil {
			rrpps = loaded
		}
	}
	h.enrichEventImage(r.Context(), event)
	h.resolveRRPPAvatars(r.Context(), rrpps)
	response := eventDetailResponse{Event: event, TempCrews: waitCrews(), RRPPs: rrpps}
	if response.TempCrews != nil {
		for i := range response.TempCrews.Preview {
			signBannerRef(r.Context(), h.s3Service, &response.TempCrews.Preview[i].Event)
		}
	}
	respondWithJSON(w, http.StatusOK, response)
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

// ClearRSVP: DELETE /v1/events/{id}/rsvp. Quita el "voy"/"no voy" de quien consulta; idempotente.
func (h *EventHandler) ClearRSVP(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	goingCount, notGoingCount, err := h.eventService.ClearRSVP(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Evento o usuario no encontrado")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error quitando el RSVP")
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
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	comments, total, err := h.eventService.GetEventComments(r.Context(), chi.URLParam(r, "id"), viewerID, limit, offset)
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
	if errors.Is(err, domain.ErrContentPolicyViolation) {
		respondWithError(w, http.StatusUnprocessableEntity, "Este mensaje infringe nuestras políticas de violencia")
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

func (h *EventHandler) GetUserEvents(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		respondWithError(w, http.StatusBadRequest, "Username is required")
		return
	}

	events, err := h.eventService.GetUserEvents(r.Context(), username)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if events == nil {
		events = []domain.Event{}
	}

	h.enrichEventImages(r.Context(), events)

	respondWithJSON(w, http.StatusOK, events)
}

func (h *EventHandler) GetLiveEventStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}

	hasLocation := r.URL.Query().Get("hasLocation") != "false"
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")

	var lat, lng float64
	if hasLocation {
		if latStr == "" || lngStr == "" {
			respondWithError(w, http.StatusBadRequest, "lat y lng son requeridos")
			return
		}

		var errLat, errLng error
		lat, errLat = strconv.ParseFloat(latStr, 64)
		lng, errLng = strconv.ParseFloat(lngStr, 64)

		if errLat != nil || errLng != nil || math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			respondWithError(w, http.StatusBadRequest, "lat y lng deben ser números válidos")
			return
		}
	}

	var (
		event     *domain.Event
		userSteps int
		err       error
		isAtVenue bool
	)

	if hasLocation {
		event, userSteps, err = h.eventService.GetLiveEventStatus(r.Context(), userID, lat, lng)
		if err == nil && event != nil {
			isAtVenue = true
		}
	} else {
		err = sql.ErrNoRows
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Si no está en el predio (o aún no arrancó la ventana geofence), buscar si tiene un evento HOY con RSVP 'going'
			if todayProvider, ok := h.eventService.(interface {
				GetTodayGoingEvent(ctx context.Context, userID string) (*domain.Event, error)
			}); ok {
				if todayEvent, todayErr := todayProvider.GetTodayGoingEvent(r.Context(), userID); todayErr == nil && todayEvent != nil {
					h.enrichEventImage(r.Context(), todayEvent)
					respondWithJSON(w, http.StatusOK, map[string]interface{}{
						"isLive":              true,
						"isAtVenue":           false,
						"attendanceConfirmed": false,
						"userSteps":           0,
						"event": map[string]interface{}{
							"id":        todayEvent.ID,
							"title":     todayEvent.Title,
							"date":      todayEvent.Date,
							"endDate":   todayEvent.EndDate,
							"location":  todayEvent.Location,
							"lineup":    todayEvent.Lineup,
							"genre":     todayEvent.Genre,
							"bannerUrl": todayEvent.CinematicBannerURL,
							"latitude":  todayEvent.Latitude,
							"longitude": todayEvent.Longitude,
						},
					})
					return
				}
			}

			respondWithJSON(w, http.StatusOK, map[string]interface{}{
				"isLive": false,
				"event":  nil,
			})
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo el estado en vivo")
		return
	}

	h.enrichEventImage(r.Context(), event)

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"isLive":              true,
		"isAtVenue":           isAtVenue,
		"attendanceConfirmed": true,
		"userSteps":           userSteps,
		"event": map[string]interface{}{
			"id":                   event.ID,
			"title":                event.Title,
			"date":                 event.Date,
			"endDate":              event.EndDate,
			"location":             event.Location,
			"lineup":               event.Lineup,
			"genre":                event.Genre,
			"bannerUrl":            event.CinematicBannerURL,
			"latitude":             event.Latitude,
			"longitude":            event.Longitude,
			"geofenceRadiusMeters": event.GeofenceRadiusMeters,
		},
	})
}

// BulkCreateEventsCSV lee un archivo CSV subido por multipart form y crea los eventos
// Solo ADMIN: antes estaba abierto y cualquiera podía cargar fiestas en producción.
func (h *EventHandler) BulkCreateEventsCSV(w http.ResponseWriter, r *http.Request) {
	if !h.requireRole(w, r, "Solo un administrador puede importar eventos", "ADMIN") {
		return
	}
	// Parsear el form (hasta 10MB)
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20+(1<<20))
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "No se pudo parsear el formulario")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Se requiere el archivo 'file'")
		return
	}
	defer file.Close()

	count, err := h.eventService.BulkCreateFromCSV(r.Context(), file)
	if err != nil {
		log.Printf("Error processing CSV: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Error procesando el archivo CSV")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Eventos creados exitosamente",
		"count":   count,
	})
}

const (
	maxEventImageBytes   = 10 << 20 // por imagen; el frontend ya manda JPEG comprimido
	maxEventImagesPerReq = 10
)

// requireRole corta la request con 401/403 si quien llama no tiene alguno de los roles.
func (h *EventHandler) requireRole(w http.ResponseWriter, r *http.Request, forbiddenMsg string, roles ...string) bool {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if h.userRepo == nil || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "No autorizado")
		return false
	}
	user, err := h.userRepo.GetUserByID(r.Context(), userID)
	// El repositorio devuelve "user not found" (sin error tipado) cuando la cuenta no existe: eso es 403.
	if err != nil && !errors.Is(err, sql.ErrNoRows) && err.Error() != "user not found" {
		log.Printf("requireRole: loading user %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo verificar el permiso")
		return false
	}
	if user == nil || !hasRole(user.Role, roles...) {
		respondWithError(w, http.StatusForbidden, forbiddenMsg)
		return false
	}
	return true
}

// UploadEventImage sube imágenes de eventos (ej. banner) al S3 y devuelve su key y URL.
// Solo ADMIN o RRPP; solo JPG/PNG/WebP detectados por contenido; la key no usa el nombre del cliente.
func (h *EventHandler) UploadEventImage(w http.ResponseWriter, r *http.Request) {
	if h.s3Service == nil {
		respondWithError(w, http.StatusInternalServerError, "S3 Service not initialized")
		return
	}
	if !h.requireRole(w, r, "Solo un administrador o RRPP puede subir imágenes de eventos", "ADMIN", "RRPP") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEventImageBytes*maxEventImagesPerReq+(1<<20))
	if err := r.ParseMultipartForm(maxEventImageBytes); err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large or invalid")
		return
	}

	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		respondWithError(w, http.StatusBadRequest, "No image files provided (use 'images' form key)")
		return
	}
	if len(files) > maxEventImagesPerReq {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Máximo %d imágenes por vez", maxEventImagesPerReq))
		return
	}

	type UploadResult struct {
		URL string `json:"url"`
		Key string `json:"key"`
	}
	results := []UploadResult{}

	for i, header := range files {
		file, err := header.Open()
		if err != nil {
			log.Printf("Error opening event image %d: %v", i, err)
			continue
		}
		data, contentType, ext, readErr := readImageUpload(file, maxEventImageBytes, false)
		file.Close()
		if readErr != nil {
			status, msg := imageUploadErrorStatus(readErr)
			respondWithError(w, status, msg)
			return
		}

		// time.Now().UnixNano() evita colisiones si se procesan en el mismo milisegundo
		key := fmt.Sprintf("events/%d_%d.%s", time.Now().UnixNano(), i, ext)
		url, s3Err := h.s3Service.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType)
		if s3Err != nil {
			log.Printf("Failed to upload event image %s to S3: %v", key, s3Err)
			continue
		}

		results = append(results, UploadResult{
			URL: url,
			Key: key,
		})
	}

	respondWithJSON(w, http.StatusOK, results)
}

// UpdateEventImage reemplaza el flyer de un evento existente (PATCH /events/{id}/image).
// Solo para usuarios ADMIN. Recibe una imagen en el campo multipart "image".
func (h *EventHandler) UpdateEventImage(w http.ResponseWriter, r *http.Request) {
	if h.s3Service == nil {
		respondWithError(w, http.StatusInternalServerError, "S3 Service not initialized")
		return
	}
	if !h.requireRole(w, r, "Solo un administrador puede cambiar el flyer de un evento", "ADMIN") {
		return
	}

	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "ID de evento requerido")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 15<<20+(1<<20))
	if err := r.ParseMultipartForm(15 << 20); err != nil {
		respondWithError(w, http.StatusBadRequest, "Imagen demasiado grande o inválida")
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Falta la imagen (campo 'image')")
		return
	}
	defer file.Close()
	data, contentType, ext, err := readImageUpload(file, 15<<20, false)
	if err != nil {
		status, msg := imageUploadErrorStatus(err)
		respondWithError(w, status, msg)
		return
	}

	key := fmt.Sprintf("events/%s/%d.%s", eventID, time.Now().UnixNano(), ext)
	if _, err := h.s3Service.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType); err != nil {
		log.Printf("Failed to upload banner for event %s: %v", eventID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo subir la imagen")
		return
	}
	if err := h.eventService.UpdateEventBanner(r.Context(), eventID, key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Evento no encontrado")
			return
		}
		log.Printf("Failed to update banner for event %s: %v", eventID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo actualizar el evento")
		return
	}

	viewURL, _ := h.s3Service.GenerateViewUrl(r.Context(), key, 7*24*time.Hour)
	respondWithJSON(w, http.StatusOK, map[string]string{"id": eventID, "key": key, "cinematicBannerUrl": viewURL})
}

func (h *EventHandler) enrichEventImages(ctx context.Context, events []domain.Event) {
	resolveEventImages(ctx, h.s3Service, events)
}

// resolveEventImages replaces the S3 keys stored for banners and artist avatars
// with URLs a client can load. Every endpoint that returns events must apply it:
// the web app only draws images whose URL starts with http.
func resolveEventImages(ctx context.Context, storage s3.S3Service, events []domain.Event) {
	if storage == nil {
		return
	}
	for i := range events {
		if events[i].CinematicBannerURL != "" && !strings.HasPrefix(events[i].CinematicBannerURL, "http") {
			if url, err := storage.GenerateViewUrl(ctx, events[i].CinematicBannerURL, 7*24*time.Hour); err == nil {
				events[i].CinematicBannerURL = url
			}
		}
		for j := range events[i].Artists {
			if events[i].Artists[j].Artist != nil && events[i].Artists[j].Artist.AvatarURL != nil {
				raw := *events[i].Artists[j].Artist.AvatarURL
				if raw != "" && !strings.HasPrefix(raw, "http") {
					if url, err := storage.GenerateViewUrl(ctx, raw, 7*24*time.Hour); err == nil {
						events[i].Artists[j].Artist.AvatarURL = &url
					}
				}
			}
		}
	}
}

// resolveRRPPAvatars convierte las claves de S3 guardadas en users.avatar_url en URLs cargables.
func (h *EventHandler) resolveRRPPAvatars(ctx context.Context, rrpps []domain.EventRRPP) {
	if h.s3Service == nil {
		return
	}
	for i := range rrpps {
		if rrpps[i].AvatarURL == nil {
			continue
		}
		raw := *rrpps[i].AvatarURL
		if raw == "" || strings.HasPrefix(raw, "http") {
			continue
		}
		if url, err := h.s3Service.GenerateViewUrl(ctx, raw, 7*24*time.Hour); err == nil {
			rrpps[i].AvatarURL = &url
		}
	}
}

func (h *EventHandler) enrichEventImage(ctx context.Context, event *domain.Event) {
	if h.s3Service == nil || event == nil {
		return
	}
	if event.CinematicBannerURL != "" && !strings.HasPrefix(event.CinematicBannerURL, "http") {
		if url, err := h.s3Service.GenerateViewUrl(ctx, event.CinematicBannerURL, 7*24*time.Hour); err == nil {
			event.CinematicBannerURL = url
		}
	}
	for j := range event.Artists {
		if event.Artists[j].Artist != nil && event.Artists[j].Artist.AvatarURL != nil {
			raw := *event.Artists[j].Artist.AvatarURL
			if raw != "" && !strings.HasPrefix(raw, "http") {
				if url, err := h.s3Service.GenerateViewUrl(ctx, raw, 7*24*time.Hour); err == nil {
					event.Artists[j].Artist.AvatarURL = &url
				}
			}
		}
	}
}
