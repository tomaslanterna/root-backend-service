package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type ChatHandler struct {
	chatService ports.ChatService
}

func NewChatHandler(chatService ports.ChatService) *ChatHandler {
	return &ChatHandler{
		chatService: chatService,
	}
}

func (h *ChatHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "id")
	afterTimestamp := r.URL.Query().Get("after_timestamp")
	currentUserID := r.Context().Value(UserIDKey).(string)

	messages, err := h.chatService.GetMessages(r.Context(), chatID, afterTimestamp, currentUserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, messages)
}

type SendMessageRequest struct {
	Content string             `json:"content"`
	Type    domain.MessageType `json:"type"`
}

func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if req.Type == "" {
		req.Type = domain.MessageTypeText
	}

	msg, err := h.chatService.SendMessage(r.Context(), chatID, currentUserID, req.Content, req.Type)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusCreated, msg)
}

func (h *ChatHandler) GetUserChats(w http.ResponseWriter, r *http.Request) {
	currentUserID := r.Context().Value(UserIDKey).(string)

	chats, err := h.chatService.GetUserChats(r.Context(), currentUserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, chats)
}

func (h *ChatHandler) GetChatByID(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	chat, err := h.chatService.GetChatByID(r.Context(), chatID, currentUserID)
	if err != nil {
		if err.Error() == "unauthorized: you are not a participant in this chat" {
			respondWithError(w, http.StatusForbidden, err.Error())
		} else if err.Error() == "chat not found" {
			respondWithError(w, http.StatusNotFound, err.Error())
		} else {
			respondWithError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	respondWithJSON(w, http.StatusOK, chat)
}

type CreateDirectChatRequest struct {
	TargetUserID string `json:"target_user_id"`
}

func (h *ChatHandler) CreateDirectChat(w http.ResponseWriter, r *http.Request) {
	currentUserID := r.Context().Value(UserIDKey).(string)

	var req CreateDirectChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	
	if req.TargetUserID == "" {
		respondWithError(w, http.StatusBadRequest, "target_user_id is required")
		return
	}

	chat, err := h.chatService.GetOrCreateDirectChat(r.Context(), currentUserID, req.TargetUserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, chat)
}
