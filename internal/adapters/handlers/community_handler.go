package handlers

import (
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type CommunityHandler struct {
	communityService ports.CommunityService
}

func NewCommunityHandler(communityService ports.CommunityService) *CommunityHandler {
	return &CommunityHandler{
		communityService: communityService,
	}
}

func (h *CommunityHandler) GetCommunities(w http.ResponseWriter, r *http.Request) {
	countryID := r.URL.Query().Get("countryId")
	if countryID == "" {
		http.Error(w, "countryId is required", http.StatusBadRequest)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	userID, _ := r.Context().Value(UserIDKey).(string)

	communities, err := h.communityService.GetCommunitiesByCountry(r.Context(), countryID, userID, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if communities == nil {
		communities = make([]domain.Community, 0)
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": communities,
	})
}

func (h *CommunityHandler) GetCommunityByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value(UserIDKey).(string)

	community, err := h.communityService.GetCommunityByID(r.Context(), id, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusOK, community)
}

func (h *CommunityHandler) JoinCommunity(w http.ResponseWriter, r *http.Request) {
	communityID := chi.URLParam(r, "id")
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "Usuario no autenticado", http.StatusUnauthorized)
		return
	}

	isMember, membersCount, err := h.communityService.ToggleJoinCommunity(r.Context(), communityID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"isMember":     isMember,
		"membersCount": membersCount,
	})
}

func (h *CommunityHandler) GetUserCommunities(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		respondWithError(w, http.StatusBadRequest, "Username is required")
		return
	}

	communities, err := h.communityService.GetUserCommunities(r.Context(), username)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if communities == nil {
		communities = []domain.Community{}
	}

	respondWithJSON(w, http.StatusOK, communities)
}
