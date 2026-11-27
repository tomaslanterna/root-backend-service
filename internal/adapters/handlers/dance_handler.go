package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

type DanceHandler struct {
	danceService ports.DanceService
	s3Service    s3.S3Service
	tempCrews    ports.TempCrewService
}

// SetTempCrewService habilita el soporte de crews temporales en los endpoints compartidos de /v1/crews.
func (h *DanceHandler) SetTempCrewService(svc ports.TempCrewService) { h.tempCrews = svc }

// tempCrewDetail devuelve los datos de la crew si es temporal, o nil si es permanente.
func (h *DanceHandler) tempCrewDetail(ctx context.Context, squadID, viewerID string) (*domain.TempCrewDetail, error) {
	if h.tempCrews == nil {
		return nil, nil
	}
	return h.tempCrews.Detail(ctx, squadID, viewerID)
}

func NewDanceHandler(svc ports.DanceService, s3Services ...s3.S3Service) *DanceHandler {
	var s3Svc s3.S3Service
	if len(s3Services) > 0 {
		s3Svc = s3Services[0]
	}
	return &DanceHandler{danceService: svc, s3Service: s3Svc}
}

func (h *DanceHandler) enrichCrewEventImages(ctx context.Context, events []map[string]interface{}) {
	if h.s3Service == nil {
		return
	}
	for i := range events {
		var raw string
		switch v := events[i]["cinematicBannerUrl"].(type) {
		case string:
			raw = strings.TrimSpace(v)
		case *string:
			if v != nil {
				raw = strings.TrimSpace(*v)
			}
		}
		if raw != "" && !strings.HasPrefix(raw, "http") {
			if url, err := h.s3Service.GenerateViewUrl(ctx, raw, 7*24*time.Hour); err == nil {
				events[i]["cinematicBannerUrl"] = url
			}
		}
	}
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
		switch {
		case errors.Is(err, ports.ErrInvalidDanceSync):
			respondWithError(w, http.StatusBadRequest, "Datos de pasos inválidos")
		case errors.Is(err, ports.ErrDanceEventNotFound):
			respondWithError(w, http.StatusNotFound, "Evento no encontrado")
		default:
			log.Printf("SyncSteps failed for user %s: %v", userID, err)
			respondWithError(w, http.StatusInternalServerError, "No se pudieron guardar los pasos")
		}
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
		IsPublic  bool     `json:"isPublic"`
		IsPublic2 bool     `json:"is_public"`
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
		IsPublic:  body.IsPublic || body.IsPublic2,
		Type:      body.Type,
		MemberIDs: memberIDs,
		EventID:   eventID,
		EventIDs:  eventIDs,
	})
	if errors.Is(err, domain.ErrContentPolicyViolation) {
		respondWithContentPolicy(w)
		return
	}
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

	// Solo vale el código de invitación (el id de la crew ya no sirve para entrar).
	codeOrID := strings.TrimSpace(chi.URLParam(r, "inviteCode"))
	if codeOrID == "" {
		var body struct {
			InviteCode string `json:"inviteCode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		codeOrID = strings.TrimSpace(body.InviteCode)
	}
	if codeOrID == "" {
		respondWithError(w, http.StatusBadRequest, "inviteCode is required")
		return
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

func (h *DanceHandler) AddCrewMembers(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	squadID := strings.TrimSpace(chi.URLParam(r, "id"))
	var body struct {
		MemberIDs []string `json:"member_ids"`
		MemberIds []string `json:"memberIds"`
		UserID    string   `json:"user_id"`
		UserId    string   `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	memberIDs := append(body.MemberIDs, body.MemberIds...)
	if body.UserID != "" {
		memberIDs = append(memberIDs, body.UserID)
	}
	if body.UserId != "" {
		memberIDs = append(memberIDs, body.UserId)
	}
	if len(memberIDs) == 0 {
		respondWithError(w, http.StatusBadRequest, "memberIds is required")
		return
	}

	crew, err := h.danceService.AddCrewMembers(r.Context(), squadID, memberIDs, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Crew not found")
			return
		}
		if errors.Is(err, domain.ErrTempCrewManaged) {
			respondWithError(w, http.StatusConflict, "Las crews temporales solo admiten gente con solicitudes")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Failed to add members to crew")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"crew":    crew,
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
		if errors.Is(err, domain.ErrTempCrewManaged) {
			respondWithError(w, http.StatusConflict, "Las crews temporales están atadas a un solo evento")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Failed to associate event with crew")
		return
	}

	events, err := h.danceService.GetCrewEvents(r.Context(), squadID)
	if err != nil {
		events = []map[string]interface{}{}
	}
	h.enrichCrewEventImages(r.Context(), events)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": events})
}

func (h *DanceHandler) GetCrewLeaderboards(w http.ResponseWriter, r *http.Request) {
	squadID := chi.URLParam(r, "id")
	eventID := r.URL.Query().Get("eventId")

	var eventIDPtr *string
	if eventID != "" {
		eventIDPtr = &eventID
	}

	// En una crew temporal el ranking es solo para integrantes.
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	if detail, err := h.tempCrewDetail(r.Context(), squadID, viewerID); err != nil {
		writeTempCrewError(w, err)
		return
	} else if detail != nil && detail.ViewerRole == nil {
		respondWithError(w, http.StatusForbidden, "Solo los integrantes pueden ver el ranking")
		return
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
		// Listado público: sin códigos de invitación.
		for _, c := range crews {
			hideInviteCode(c)
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

// JoinPublicCrew: POST /crews/{id}/join. Unirse a una crew pública sin código.
// Una privada responde 404: a esas solo se entra con el link de invitación.
func (h *DanceHandler) JoinPublicCrew(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}
	squadID := strings.TrimSpace(chi.URLParam(r, "id"))
	crew, err := h.danceService.JoinPublicCrew(r.Context(), userID, squadID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Crew not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Failed to join crew")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"success": true, "crewId": crew["id"], "crew": crew})
}

// UpdateCrewVisibility: PATCH /crews/{id} con {"isPublic": bool}. Solo quien creó la crew.
func (h *DanceHandler) UpdateCrewVisibility(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}
	var body struct {
		IsPublic    *bool `json:"isPublic"`
		IsAdmitting *bool `json:"isAdmitting"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.IsPublic == nil && body.IsAdmitting == nil) {
		respondWithError(w, http.StatusBadRequest, "isPublic or isAdmitting is required")
		return
	}
	squadID := strings.TrimSpace(chi.URLParam(r, "id"))
	// Crews temporales: PATCH abre o cierra las solicitudes. Solo el host.
	if body.IsAdmitting != nil && h.tempCrews != nil {
		admitting, err := h.tempCrews.SetAdmitting(r.Context(), squadID, userID, *body.IsAdmitting)
		if err != nil {
			writeTempCrewError(w, err)
			return
		}
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"isAdmitting": admitting})
		return
	}
	if body.IsPublic == nil {
		respondWithError(w, http.StatusBadRequest, "isPublic is required")
		return
	}
	err := h.danceService.SetCrewVisibility(r.Context(), squadID, userID, *body.IsPublic)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		respondWithError(w, http.StatusNotFound, "Crew not found")
	case errors.Is(err, ports.ErrNotCrewHost):
		respondWithError(w, http.StatusForbidden, "Solo quien creó la crew puede cambiar su visibilidad")
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "No se pudo cambiar la visibilidad")
	default:
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"id": squadID, "isPublic": *body.IsPublic})
	}
}

// hideInviteCode saca el código de invitación de una crew: solo lo ven los miembros.
func hideInviteCode(crew map[string]interface{}) {
	delete(crew, "inviteCode")
	delete(crew, "invite_code")
}

// PreviewCrewInvite devuelve los datos básicos de la crew dueña de un código, para mostrar la invitación
// antes de unirse. Es público: quien recibe el link puede verla aunque todavía no tenga sesión.
func (h *DanceHandler) PreviewCrewInvite(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(chi.URLParam(r, "code"))
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	preview, err := h.danceService.GetCrewInvitePreview(r.Context(), code, viewerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Este link de invitación no es válido o ya fue reemplazado")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "No se pudo cargar la invitación")
		return
	}
	if h.s3Service != nil {
		if members, ok := preview["members"].([]map[string]interface{}); ok {
			for _, m := range members {
				if avatar, ok := m["avatarUrl"].(*string); ok && avatar != nil && *avatar != "" && !strings.HasPrefix(*avatar, "http") {
					if url, err := h.s3Service.GenerateViewUrl(r.Context(), *avatar, 7*24*time.Hour); err == nil {
						m["avatarUrl"] = url
					}
				}
			}
		}
	}
	respondWithJSON(w, http.StatusOK, preview)
}

// RegenerateInviteCode crea un link de invitación nuevo y anula el anterior. Cualquier miembro puede hacerlo.
func (h *DanceHandler) RegenerateInviteCode(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}
	squadID := strings.TrimSpace(chi.URLParam(r, "id"))
	code, err := h.danceService.RegenerateCrewInviteCode(r.Context(), squadID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Crew not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "No se pudo regenerar el link")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"inviteCode": code})
}

func (h *DanceHandler) GetCrewByID(w http.ResponseWriter, r *http.Request) {
	squadID := chi.URLParam(r, "id")
	crew, err := h.danceService.GetCrewByID(r.Context(), squadID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Crew not found")
		return
	}
	// El código de invitación es solo para miembros: quien ve la crew sin serlo no puede unirse con él.
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	if member, err := h.danceService.IsCrewMember(r.Context(), squadID, viewerID); err != nil || !member {
		hideInviteCode(crew)
	}
	// Crew temporal: se descubre desde el evento, así que la ve cualquiera con sesión (también después de que
	// termina: sigue en la app, solo que ya no admite gente). Nunca expone código de invitación: el ingreso es
	// siempre con aprobación.
	if detail, err := h.tempCrewDetail(r.Context(), squadID, viewerID); err != nil {
		writeTempCrewError(w, err)
		return
	} else if detail != nil {
		if viewerID == "" {
			respondWithError(w, http.StatusUnauthorized, "User not authenticated")
			return
		}
		hideInviteCode(crew)
		signBannerRef(r.Context(), h.s3Service, &detail.Event)
		crew["tempCrew"] = detail
	}
	if evs, ok := crew["events"].([]map[string]interface{}); ok {
		h.enrichCrewEventImages(r.Context(), evs)
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
	h.enrichCrewEventImages(r.Context(), events)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": events})
}

func (h *DanceHandler) GetEventLiveRanking(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(chi.URLParam(r, "id"))
	if eventID == "" {
		respondWithError(w, http.StatusBadRequest, "eventId is required")
		return
	}

	result, err := h.danceService.GetEventLiveRanking(r.Context(), eventID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get event live ranking")
		return
	}

	respondWithJSON(w, http.StatusOK, result)
}
