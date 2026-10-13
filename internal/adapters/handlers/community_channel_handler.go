package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
)

func (h *CommunityHandler) GetChatSettings(w http.ResponseWriter, r *http.Request) {
	communityID := chi.URLParam(r, "id")
	settings, err := h.communityService.GetChatSettings(r.Context(), communityID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, settings)
}

func (h *CommunityHandler) UpdateChatSettings(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Mode) == "" {
		respondWithError(w, http.StatusBadRequest, "mode es obligatorio")
		return
	}

	communityID := chi.URLParam(r, "id")
	settings, err := h.communityService.UpdateChatSettings(r.Context(), communityID, u, body.Mode)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, settings)
}

func (h *CommunityHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	communityID := chi.URLParam(r, "id")

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if l, err := strconv.Atoi(raw); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))

	messages, err := h.communityService.GetCommunityMessages(r.Context(), communityID, userID, limit, before)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	if messages == nil {
		messages = []domain.CommunityChatMessage{}
	}
	respondWithJSON(w, http.StatusOK, messages)
}

func (h *CommunityHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		respondWithError(w, http.StatusBadRequest, "content no puede estar vacío")
		return
	}

	communityID := chi.URLParam(r, "id")
	msg, err := h.communityService.SendCommunityMessage(r.Context(), communityID, u, body.Content)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusCreated, msg)
}

func (h *CommunityHandler) ToggleReaction(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Emoji string `json:"emoji"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Emoji) == "" {
		respondWithError(w, http.StatusBadRequest, "emoji es obligatorio")
		return
	}

	communityID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	reactions, err := h.communityService.ToggleMessageReaction(r.Context(), communityID, messageID, u, body.Emoji)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"messageId": messageID,
		"reactions": reactions,
	})
}

func (h *CommunityHandler) GetMembers(w http.ResponseWriter, r *http.Request) {
	communityID := chi.URLParam(r, "id")
	limit, offset, err := parseCommunityPagination(r, 20)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := r.URL.Query().Get("query")

	members, total, err := h.communityService.GetCommunityMembers(r.Context(), communityID, limit, offset, query)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	if members == nil {
		members = []domain.CommunityMember{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": members,
		"meta": map[string]interface{}{
			"total":   total,
			"limit":   limit,
			"offset":  offset,
			"hasMore": offset+len(members) < total,
		},
	})
}

func (h *CommunityHandler) KickMember(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	communityID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userId")

	remainingCount, err := h.communityService.KickCommunityMember(r.Context(), communityID, u, targetUserID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":               true,
		"removedUserId":         targetUserID,
		"remainingMembersCount": remainingCount,
	})
}

func (h *CommunityHandler) GetPromotedEvents(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	communityID := chi.URLParam(r, "id")

	events, err := h.communityService.GetPromotedEvents(r.Context(), communityID, userID)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	if events == nil {
		events = []domain.Event{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
	})
}

func (h *CommunityHandler) UpdatePromotedEvents(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		EventIDs []string `json:"eventIds"`
	}
	if !communityBody(w, r, &body) {
		return
	}

	communityID := chi.URLParam(r, "id")
	events, err := h.communityService.UpdatePromotedEvents(r.Context(), communityID, u, body.EventIDs)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	if events == nil {
		events = []domain.Event{}
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": events,
	})
}
