package handlers

import (
	"encoding/json"
	"github.com/google/uuid"
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
	if before := r.URL.Query().Get("before"); before != "" {
		afterTimestamp = "before:" + before
	}
	if _, err := uuid.Parse(chatID); err != nil {
		respondWithError(w, 400, "Invalid chat ID")
		return
	}
	currentUserID := r.Context().Value(UserIDKey).(string)

	messages, err := h.chatService.GetMessages(r.Context(), chatID, afterTimestamp, currentUserID)
	if err != nil {
		respondWithChatError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, messages)
}

type SendMessageRequest struct {
	Content         string             `json:"content"`
	Type            domain.MessageType `json:"type"`
	ClientMessageID string             `json:"client_message_id"`
}

func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	var req SendMessageRequest
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if req.Type == "" {
		req.Type = domain.MessageTypeText
	}

	if _, err := uuid.Parse(chatID); err != nil {
		respondWithError(w, 400, "Invalid chat ID")
		return
	}
	msg, err := h.chatService.SendMessage(r.Context(), chatID, currentUserID, req.Content, req.Type, req.ClientMessageID)
	if err != nil {
		respondWithChatError(w, err)
		return
	}

	respondWithJSON(w, http.StatusCreated, msg)
}

func (h *ChatHandler) MarkMessagesRead(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	if err := h.chatService.MarkMessagesRead(r.Context(), chatID, currentUserID); err != nil {
		respondWithChatError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func respondWithChatError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case "message content cannot be empty", "invalid message content or type", "invalid client_message_id", "invalid message cursor", "invalid message IDs":
		respondWithError(w, http.StatusBadRequest, err.Error())
	case "client_message_id already used":
		respondWithError(w, http.StatusConflict, err.Error())
	case "unauthorized: you are not a participant in this chat":
		respondWithError(w, http.StatusForbidden, err.Error())
	case "chat not found":
		respondWithError(w, http.StatusNotFound, err.Error())
	default:
		respondWithError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *ChatHandler) AcknowledgeMessages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs  []string `json:"message_ids"`
		Read bool     `json:"read"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, 400, "Invalid receipt")
		return
	}
	if _, err := uuid.Parse(chi.URLParam(r, "id")); err != nil {
		respondWithError(w, 400, "Invalid chat ID")
		return
	}
	if err := h.chatService.AcknowledgeMessages(r.Context(), chi.URLParam(r, "id"), r.Context().Value(UserIDKey).(string), req.IDs, req.Read); err != nil {
		respondWithChatError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		respondWithChatError(w, err)
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
