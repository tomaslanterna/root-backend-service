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
