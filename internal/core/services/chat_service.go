package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"

	"github.com/google/uuid"
)

type chatService struct {
	chatRepo    ports.ChatRepository
	messageRepo ports.MessageRepository
}

func NewChatService(chatRepo ports.ChatRepository, messageRepo ports.MessageRepository) ports.ChatService {
	return &chatService{
		chatRepo:    chatRepo,
		messageRepo: messageRepo,
	}
}

func (s *chatService) GetMessages(ctx context.Context, chatID string, afterTimestamp string, currentUserID string) ([]domain.Message, error) {
	// In a real app we might verify if currentUserID is a participant of chatID
	return s.messageRepo.GetMessagesByChatID(ctx, chatID, afterTimestamp)
}

func (s *chatService) SendMessage(ctx context.Context, chatID, currentUserID, content string, msgType domain.MessageType) (*domain.Message, error) {
	if content == "" {
		return nil, errors.New("message content cannot be empty")
	}

	msg := &domain.Message{
		ID:        uuid.New().String(),
		ChatID:    chatID,
		SenderID:  currentUserID,
		Content:   content,
		Type:      msgType,
		Timestamp: time.Now(),
	}

	if err := s.messageRepo.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}

	// Update last message in chat
	if err := s.chatRepo.UpdateLastMessage(ctx, chatID, content); err != nil {
		return nil, err
	}

	return msg, nil
}
