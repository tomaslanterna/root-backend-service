package handlers

import (
	"errors"
	"log"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

type CommunityHandler struct {
	communityService ports.CommunityService
}

func NewCommunityHandler(communityService ports.CommunityService) *CommunityHandler {
	return &CommunityHandler{communityService: communityService}
}

func parseCommunityPagination(r *http.Request, defaultLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			return 0, 0, errors.New("limit must be between 1 and 50")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be non-negative")
		}
	}
	return limit, offset, nil
}

func (h *CommunityHandler) GetCommunities(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseCommunityPagination(r, 12)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	country := strings.TrimSpace(r.URL.Query().Get("country"))
	if country == "" {
		country = strings.TrimSpace(r.URL.Query().Get("countryId"))
	}
	department := strings.TrimSpace(r.URL.Query().Get("department"))
	if department == "" {
		department = strings.TrimSpace(r.URL.Query().Get("zone"))
	}
	filter := domain.CommunityFilter{
		Country:    country,
		Category:   r.URL.Query().Get("category"),
		Department: department,
		Query:      r.URL.Query().Get("query"),
		Limit:      limit,
		Offset:     offset,
		Scope:      r.URL.Query().Get("scope"),
	}
	userID, _ := r.Context().Value(UserIDKey).(string)
	if filter.Scope != "" && filter.Scope != "mine" && filter.Scope != "explore" {
		respondWithError(w, 400, "Scope inválido")
		return
	}
	if filter.Scope == "mine" && userID == "" {
		respondWithError(w, 401, "Usuario no autenticado")
		return
	}

	communities, total, err := h.communityService.GetCommunities(r.Context(), filter, userID)
	if err != nil {
		log.Printf("get communities: %v", err)
		respondWithError(w, http.StatusInternalServerError, "No se pudieron obtener las comunidades")
		return
	}
	if communities == nil {
		communities = []domain.Community{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": communities,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(communities) < total,
		},
	})
}

func (h *CommunityHandler) GetCommunity(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	community, err := h.communityService.GetCommunity(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, community)
}

func (h *CommunityHandler) JoinCommunity(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	membersCount, err := h.communityService.JoinCommunity(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"isMember": true, "membersCount": membersCount})
}

func (h *CommunityHandler) LeaveCommunity(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	membersCount, err := h.communityService.LeaveCommunity(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"isMember": false, "membersCount": membersCount})
}

func (h *CommunityHandler) GetUserCommunities(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	if username == "" {
		respondWithError(w, http.StatusBadRequest, "Username is required")
		return
	}
	currentUserID, _ := r.Context().Value(UserIDKey).(string)
	communities, err := h.communityService.GetUserCommunities(r.Context(), username, currentUserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron obtener las comunidades del usuario")
		return
	}
	if communities == nil {
		communities = []domain.Community{}
	}
	respondWithJSON(w, http.StatusOK, communities)
}

func (h *CommunityHandler) GetMyCommunities(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	communities, err := h.communityService.GetCurrentUserCommunities(r.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron obtener tus comunidades")
		return
	}
	if communities == nil {
		communities = []domain.Community{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": communities})
}

func respondWithCommunityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrCommunityNotFound):
		respondWithError(w, http.StatusNotFound, "Comunidad no encontrada")
	case errors.Is(err, domain.ErrCommunityForbidden):
		respondWithError(w, http.StatusForbidden, "No tenés permiso para esta acción en la comunidad")
	case errors.Is(err, domain.ErrCommunityInvalid):
		respondWithError(w, http.StatusBadRequest, "Solicitud de comunidad inválida")
	case errors.Is(err, domain.ErrCommunityTargetNotFound):
		respondWithError(w, http.StatusNotFound, "Contenido no encontrado en esta comunidad")
	default:
		respondWithError(w, http.StatusInternalServerError, "No se pudo completar la operación")
	}
}
