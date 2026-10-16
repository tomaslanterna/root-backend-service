package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type channelServiceStub struct {
	ports.CommunityService
	chatSettings    *domain.CommunityChatSettings
	messages        []domain.CommunityChatMessage
	members         []domain.CommunityMember
	promotedEvents  []domain.Event
	sentMsg         *domain.CommunityChatMessage
	updatedSettings *domain.CommunityChatSettings
	reactions       []domain.CommunityChatReaction
}

func (s *channelServiceStub) GetChatSettings(_ context.Context, id string) (*domain.CommunityChatSettings, error) {
	if s.chatSettings == nil {
		return &domain.CommunityChatSettings{CommunityID: id, Mode: "broadcast_only"}, nil
	}
	return s.chatSettings, nil
}

func (s *channelServiceStub) UpdateChatSettings(_ context.Context, id, userID, mode string) (*domain.CommunityChatSettings, error) {
	s.updatedSettings = &domain.CommunityChatSettings{CommunityID: id, Mode: mode, UpdatedBy: &userID, UpdatedAt: time.Now()}
	return s.updatedSettings, nil
}

func (s *channelServiceStub) GetCommunityMessages(_ context.Context, _ string, _ string, _ int, _ string) ([]domain.CommunityChatMessage, error) {
	return s.messages, nil
}

func (s *channelServiceStub) SendCommunityMessage(_ context.Context, id, senderID, content string) (*domain.CommunityChatMessage, error) {
	s.sentMsg = &domain.CommunityChatMessage{
		ID:          "msg-created-1",
		CommunityID: id,
		SenderID:    senderID,
		SenderRole:  "RRPP",
		Content:     content,
		Timestamp:   time.Now(),
	}
	return s.sentMsg, nil
}

func (s *channelServiceStub) ToggleMessageReaction(_ context.Context, _, _, _, emoji string) ([]domain.CommunityChatReaction, error) {
	s.reactions = []domain.CommunityChatReaction{{Emoji: emoji, Count: 1, Users: []string{"u1"}}}
	return s.reactions, nil
}

func (s *channelServiceStub) GetCommunityMembers(_ context.Context, _ string, _, _ int, _ string) ([]domain.CommunityMember, int, error) {
	return s.members, len(s.members), nil
}

func (s *channelServiceStub) KickCommunityMember(_ context.Context, _, _, targetUserID string) (int, error) {
	return 15, nil
}

func (s *channelServiceStub) GetPromotedEvents(_ context.Context, _, _ string) ([]domain.Event, error) {
	return s.promotedEvents, nil
}

func (s *channelServiceStub) UpdatePromotedEvents(_ context.Context, _, _ string, eventIDs []string) ([]domain.Event, error) {
	return s.promotedEvents, nil
}

func routeWithParams(req *http.Request, params map[string]string, userID string) *http.Request {
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	if userID != "" {
		ctx = context.WithValue(ctx, UserIDKey, userID)
	}
	return req.WithContext(ctx)
}

func TestChatSettingsHandler(t *testing.T) {
	svc := &channelServiceStub{}
	handler := NewCommunityHandler(svc)

	// GET
	req := httptest.NewRequest(http.MethodGet, "/v1/communities/c1/chat-settings", nil)
	req = routeWithParams(req, map[string]string{"id": "c1"}, "")
	rec := httptest.NewRecorder()
	handler.GetChatSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// PUT
	body, _ := json.Marshal(map[string]string{"mode": "open_chat"})
	req = httptest.NewRequest(http.MethodPut, "/v1/communities/c1/chat-settings", bytes.NewReader(body))
	req = routeWithParams(req, map[string]string{"id": "c1"}, "user-rrpp")
	rec = httptest.NewRecorder()
	handler.UpdateChatSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.updatedSettings.Mode != "open_chat" {
		t.Fatalf("expected open_chat, got %s", svc.updatedSettings.Mode)
	}
}

func TestMessagesHandler(t *testing.T) {
	svc := &channelServiceStub{
		messages: []domain.CommunityChatMessage{
			{ID: "m1", Content: "Bienvenidos", SenderRole: "RRPP"},
		},
	}
	handler := NewCommunityHandler(svc)

	// GET
	req := httptest.NewRequest(http.MethodGet, "/v1/communities/c1/messages?limit=20", nil)
	req = routeWithParams(req, map[string]string{"id": "c1"}, "user-member")
	rec := httptest.NewRecorder()
	handler.GetMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res []domain.CommunityChatMessage
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil || len(res) != 1 {
		t.Fatalf("expected 1 message, got error: %v", err)
	}

	// POST
	msgBody, _ := json.Marshal(map[string]string{"content": "Nuevo anuncio"})
	req = httptest.NewRequest(http.MethodPost, "/v1/communities/c1/messages", bytes.NewReader(msgBody))
	req = routeWithParams(req, map[string]string{"id": "c1"}, "user-rrpp")
	rec = httptest.NewRecorder()
	handler.SendMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
}

func TestToggleReactionHandler(t *testing.T) {
	svc := &channelServiceStub{}
	handler := NewCommunityHandler(svc)

	body, _ := json.Marshal(map[string]string{"emoji": "🔥"})
	req := httptest.NewRequest(http.MethodPost, "/v1/communities/c1/messages/m1/reactions", bytes.NewReader(body))
	req = routeWithParams(req, map[string]string{"id": "c1", "messageId": "m1"}, "user-member")
	rec := httptest.NewRecorder()
	handler.ToggleReaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res struct {
		MessageID string                          `json:"messageId"`
		Reactions []domain.CommunityChatReaction `json:"reactions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil || res.MessageID != "m1" || len(res.Reactions) != 1 {
		t.Fatalf("invalid reaction response: %v", err)
	}
}

func TestMembersDirectoryHandler(t *testing.T) {
	svc := &channelServiceStub{
		members: []domain.CommunityMember{
			{ID: "u1", Name: "Alex RRPP", Role: "RRPP"},
			{ID: "u2", Name: "Member One", Role: "MEMBER"},
		},
	}
	handler := NewCommunityHandler(svc)

	// GET (guest access)
	req := httptest.NewRequest(http.MethodGet, "/v1/communities/c1/members?limit=10&offset=0", nil)
	req = routeWithParams(req, map[string]string{"id": "c1"}, "")
	rec := httptest.NewRecorder()
	handler.GetMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res struct {
		Data []domain.CommunityMember `json:"data"`
		Meta map[string]interface{}   `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil || len(res.Data) != 2 {
		t.Fatalf("expected 2 members, got error: %v", err)
	}

	// DELETE (kick member)
	req = httptest.NewRequest(http.MethodDelete, "/v1/communities/c1/members/u2", nil)
	req = routeWithParams(req, map[string]string{"id": "c1", "userId": "u2"}, "user-rrpp")
	rec = httptest.NewRecorder()
	handler.KickMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestPromotedEventsHandler(t *testing.T) {
	svc := &channelServiceStub{
		promotedEvents: []domain.Event{
			{ID: "evt-1", Title: "Afterlife 2026"},
		},
	}
	handler := NewCommunityHandler(svc)

	// GET
	req := httptest.NewRequest(http.MethodGet, "/v1/communities/c1/promoted-events", nil)
	req = routeWithParams(req, map[string]string{"id": "c1"}, "user-member")
	rec := httptest.NewRecorder()
	handler.GetPromotedEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// PUT
	body, _ := json.Marshal(map[string][]string{"eventIds": {"evt-1"}})
	req = httptest.NewRequest(http.MethodPut, "/v1/communities/c1/promoted-events", bytes.NewReader(body))
	req = routeWithParams(req, map[string]string{"id": "c1"}, "user-rrpp")
	rec = httptest.NewRecorder()
	handler.UpdatePromotedEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
