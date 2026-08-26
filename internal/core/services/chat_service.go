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

func (s *chatService) GetUserChats(ctx context.Context, userID string) ([]domain.Chat, error) {
	return s.chatRepo.GetUserChats(ctx, userID)
}

func (s *chatService) GetChatByID(ctx context.Context, chatID, currentUserID string) (*domain.Chat, error) {
	chat, err := s.chatRepo.GetChatByID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	
	// Check if current user is participant
	isParticipant := false
	for _, p := range chat.Participants {
		if p.ID == currentUserID {
			isParticipant = true
			break
		}
	}
	
	if !isParticipant {
		return nil, errors.New("unauthorized: you are not a participant in this chat")
	}
	
	return chat, nil
}

func (s *chatService) GetOrCreateDirectChat(ctx context.Context, currentUserID, targetUserID string) (*domain.Chat, error) {
	if currentUserID == targetUserID {
		return nil, errors.New("cannot create a direct chat with yourself")
	}

	// 1. Check if chat already exists
	chat, err := s.chatRepo.GetDirectChatBetweenUsers(ctx, currentUserID, targetUserID)
	if err != nil {
		return nil, err
	}
	
	if chat != nil {
		return chat, nil
	}
	
	// 2. Create new chat
	newChat := &domain.Chat{
		ID:          uuid.New().String(),
		Type:        domain.ChatTypeDirect,
		LastMessage: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	
	if err := s.chatRepo.CreateChat(ctx, newChat); err != nil {
		return nil, err
	}
	
	// 3. Add participants
	p1 := &domain.ChatParticipant{
		ChatID:   newChat.ID,
		UserID:   currentUserID,
		JoinedAt: time.Now(),
	}
	p2 := &domain.ChatParticipant{
		ChatID:   newChat.ID,
		UserID:   targetUserID,
		JoinedAt: time.Now(),
	}
	
	if err := s.chatRepo.AddParticipant(ctx, p1); err != nil {
		return nil, err
	}
	if err := s.chatRepo.AddParticipant(ctx, p2); err != nil {
		return nil, err
	}
	
	return s.chatRepo.GetChatByID(ctx, newChat.ID)
}
