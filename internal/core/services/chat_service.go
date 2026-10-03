package services

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"time"
	"unicode/utf8"

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
	if _, err := s.getAuthorizedChat(ctx, chatID, currentUserID); err != nil {
		return nil, err
	}
	if afterTimestamp != "" && !strings.HasPrefix(afterTimestamp, "id:") {
		value := strings.TrimPrefix(afterTimestamp, "before:")
		parts := strings.Split(value, "|")
		if strings.HasPrefix(afterTimestamp, "before:") {
			if len(parts) != 2 {
				return nil, errors.New("invalid message cursor")
			}
			if _, err := uuid.Parse(parts[1]); err != nil {
				return nil, errors.New("invalid message cursor")
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
			return nil, errors.New("invalid message cursor")
		}
	}
	return s.messageRepo.GetMessagesByChatID(ctx, chatID, afterTimestamp, currentUserID)
}

func (s *chatService) SendMessage(ctx context.Context, chatID, currentUserID, content string, msgType domain.MessageType, clientID ...string) (*domain.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("message content cannot be empty")
	}
	if utf8.RuneCountInString(content) > 4000 || (msgType != domain.MessageTypeText && msgType != domain.MessageTypeImage) {
		return nil, errors.New("invalid message content or type")
	}
	id := uuid.New().String()
	if len(clientID) > 0 && clientID[0] != "" {
		parsed, err := uuid.Parse(clientID[0])
		if err != nil {
			return nil, errors.New("invalid client_message_id")
		}
		id = parsed.String()
	}
	if _, err := s.getAuthorizedChat(ctx, chatID, currentUserID); err != nil {
		return nil, err
	}

	msg := &domain.Message{
		ID:        id,
		ChatID:    chatID,
		SenderID:  currentUserID,
		Content:   content,
		Type:      msgType,
		Timestamp: time.Now(),
		Status:    domain.MessageStatusSent,
	}

	if err := s.messageRepo.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

func (s *chatService) AcknowledgeMessages(ctx context.Context, chatID, userID string, messageIDs []string, read bool) error {
	if _, err := s.getAuthorizedChat(ctx, chatID, userID); err != nil {
		return err
	}
	if len(messageIDs) == 0 || len(messageIDs) > 100 {
		return errors.New("invalid message IDs")
	}
	for _, id := range messageIDs {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("invalid message IDs")
		}
	}
	return s.messageRepo.AcknowledgeMessages(ctx, chatID, userID, messageIDs, read)
}

func (s *chatService) MarkMessagesRead(ctx context.Context, chatID, currentUserID string) error {
	if _, err := s.getAuthorizedChat(ctx, chatID, currentUserID); err != nil {
		return err
	}
	return s.messageRepo.MarkMessagesRead(ctx, chatID, currentUserID)
}

func (s *chatService) GetUserChats(ctx context.Context, userID string) ([]domain.Chat, error) {
	return s.chatRepo.GetUserChats(ctx, userID)
}

func (s *chatService) GetChatByID(ctx context.Context, chatID, currentUserID string) (*domain.Chat, error) {
	return s.getAuthorizedChat(ctx, chatID, currentUserID)
}

func (s *chatService) getAuthorizedChat(ctx context.Context, chatID, currentUserID string) (*domain.Chat, error) {
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
