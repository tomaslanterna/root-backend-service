package services

import (
	"context"
	"root-backend-service/internal/core/domain"
	"testing"
)

type chatRepositoryStub struct {
	chat               *domain.Chat
	updatedLastMessage string
}

func (s *chatRepositoryStub) CreateChat(context.Context, *domain.Chat) error { return nil }
func (s *chatRepositoryStub) GetChatByID(context.Context, string) (*domain.Chat, error) {
	return s.chat, nil
}
func (s *chatRepositoryStub) UpdateLastMessage(_ context.Context, _ string, message string) error {
	s.updatedLastMessage = message
	return nil
}
func (s *chatRepositoryStub) AddParticipant(context.Context, *domain.ChatParticipant) error {
	return nil
}
func (s *chatRepositoryStub) GetUserChats(context.Context, string) ([]domain.Chat, error) {
	return nil, nil
}
func (s *chatRepositoryStub) GetDirectChatBetweenUsers(context.Context, string, string) (*domain.Chat, error) {
	return nil, nil
}

type messageRepositoryStub struct {
	created     *domain.Message
	messages    []domain.Message
	markedChat  string
	markedBy    string
	markInvoked bool
}

func (s *messageRepositoryStub) InitSchema(context.Context) error { return nil }
func (s *messageRepositoryStub) AcknowledgeMessages(context.Context, string, string, []string, bool) error {
	return nil
}
func (s *messageRepositoryStub) CreateMessage(_ context.Context, message *domain.Message) error {
	s.created = message
	return nil
}
func (s *messageRepositoryStub) GetMessagesByChatID(context.Context, string, string, string) ([]domain.Message, error) {
	return s.messages, nil
}
func (s *messageRepositoryStub) MarkMessagesRead(_ context.Context, chatID, userID string) error {
	s.markInvoked = true
	s.markedChat = chatID
	s.markedBy = userID
	return nil
}

func testChatWithParticipants(ids ...string) *domain.Chat {
	participants := make([]domain.User, 0, len(ids))
	for _, id := range ids {
		participants = append(participants, domain.User{ID: id})
	}
	return &domain.Chat{ID: "chat-1", Participants: participants}
}

func TestChatServiceMarksMessagesReadForParticipant(t *testing.T) {
	chatRepo := &chatRepositoryStub{chat: testChatWithParticipants("sender", "reader")}
	messageRepo := &messageRepositoryStub{}
	service := NewChatService(chatRepo, messageRepo)

	if err := service.MarkMessagesRead(context.Background(), "chat-1", "reader"); err != nil {
		t.Fatalf("marking messages read: %v", err)
	}
	if !messageRepo.markInvoked || messageRepo.markedChat != "chat-1" || messageRepo.markedBy != "reader" {
		t.Fatalf("unexpected mark read call: invoked=%v chat=%q user=%q", messageRepo.markInvoked, messageRepo.markedChat, messageRepo.markedBy)
	}
}

func TestChatServiceHistoryDoesNotMarkRead(t *testing.T) {
	chatRepo := &chatRepositoryStub{chat: testChatWithParticipants("sender", "reader")}
	messageRepo := &messageRepositoryStub{messages: []domain.Message{{ID: "message-1"}}}
	service := NewChatService(chatRepo, messageRepo)

	messages, err := service.GetMessages(context.Background(), "chat-1", "", "reader")
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "message-1" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	if messageRepo.markInvoked {
		t.Fatal("loading history must not mark messages read")
	}
}

func TestChatServiceRejectsReadReceiptFromNonParticipant(t *testing.T) {
	chatRepo := &chatRepositoryStub{chat: testChatWithParticipants("sender", "reader")}
	messageRepo := &messageRepositoryStub{}
	service := NewChatService(chatRepo, messageRepo)

	err := service.MarkMessagesRead(context.Background(), "chat-1", "intruder")
	if err == nil || err.Error() != "unauthorized: you are not a participant in this chat" {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if messageRepo.markInvoked {
		t.Fatal("repository must not mark messages for a non-participant")
	}
}

func TestChatServiceCreatesSentMessage(t *testing.T) {
	chatRepo := &chatRepositoryStub{chat: testChatWithParticipants("sender", "reader")}
	messageRepo := &messageRepositoryStub{}
	service := NewChatService(chatRepo, messageRepo)

	message, err := service.SendMessage(context.Background(), "chat-1", "sender", "  hola  ", domain.MessageTypeText)
	if err != nil {
		t.Fatalf("sending message: %v", err)
	}
	if message.Status != domain.MessageStatusSent {
		t.Fatalf("expected sent status, got %q", message.Status)
	}
	if message.Content != "hola" {
		t.Fatalf("expected trimmed content, got message=%q last=%q", message.Content, chatRepo.updatedLastMessage)
	}
	if messageRepo.created != message {
		t.Fatal("created message was not passed to the repository")
	}
	if messageRepo.markInvoked {
		t.Fatal("sending a reply must not infer read status")
	}
}

func TestChatServiceValidatesMessageAndReceipt(t *testing.T) {
	repository := &messageRepositoryStub{}
	service := NewChatService(&chatRepositoryStub{chat: testChatWithParticipants("sender", "reader", "third")}, repository)
	for _, status := range []bool{false, true} {
		if err := service.AcknowledgeMessages(context.Background(), "chat-1", "intruder", []string{"f7619406-b313-46c8-8de1-ffb624fe47b6"}, status); err == nil {
			t.Fatal("outsider receipt accepted")
		}
	}
	if err := service.AcknowledgeMessages(context.Background(), "chat-1", "reader", []string{"invalid"}, true); err == nil {
		t.Fatal("invalid receipt accepted")
	}
	if _, err := service.SendMessage(context.Background(), "chat-1", "sender", "hello", domain.MessageTypeSystem); err == nil {
		t.Fatal("client system message accepted")
	}
	if _, err := service.SendMessage(context.Background(), "chat-1", "sender", "hello", domain.MessageTypeText, "invalid"); err == nil {
		t.Fatal("invalid idempotency key accepted")
	}
	if _, err := service.GetMessages(context.Background(), "chat-1", "before:broken", "reader"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	id := "f7619406-b313-46c8-8de1-ffb624fe47b6"
	message, err := service.SendMessage(context.Background(), "chat-1", "sender", "hello", domain.MessageTypeText, id)
	if err != nil || message.ID != id {
		t.Fatalf("idempotency key was not preserved: %#v %v", message, err)
	}
}
