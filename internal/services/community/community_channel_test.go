package community

import (
	"context"
	"errors"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type channelRepoStub struct {
	ports.CommunityRepository
	community       *domain.Community
	chatSettings    *domain.CommunityChatSettings
	messages        []domain.CommunityChatMessage
	members         []domain.CommunityMember
	promotedEvents  []domain.Event
	sentMessage     *domain.CommunityChatMessage
	updatedSettings *domain.CommunityChatSettings
	updatedEvents   []string
	kickedUser      string
	toggledEmoji    string
}

func (s *channelRepoStub) GetCommunityByIDOrSlug(_ context.Context, id, _ string) (*domain.Community, error) {
	if s.community == nil {
		return nil, domain.ErrCommunityNotFound
	}
	return s.community, nil
}

func (s *channelRepoStub) GetChatSettings(_ context.Context, id string) (*domain.CommunityChatSettings, error) {
	if s.chatSettings == nil {
		return &domain.CommunityChatSettings{CommunityID: id, Mode: "broadcast_only"}, nil
	}
	return s.chatSettings, nil
}

func (s *channelRepoStub) UpdateChatSettings(_ context.Context, id, userID, mode string) (*domain.CommunityChatSettings, error) {
	s.updatedSettings = &domain.CommunityChatSettings{CommunityID: id, Mode: mode, UpdatedBy: &userID, UpdatedAt: time.Now()}
	return s.updatedSettings, nil
}

func (s *channelRepoStub) GetCommunityMessages(_ context.Context, _ string, _ int, _ string) ([]domain.CommunityChatMessage, error) {
	return s.messages, nil
}

func (s *channelRepoStub) SendCommunityMessage(_ context.Context, communityID, senderID, content string) (*domain.CommunityChatMessage, error) {
	s.sentMessage = &domain.CommunityChatMessage{
		ID:          "msg-new",
		CommunityID: communityID,
		SenderID:    senderID,
		Content:     content,
		Timestamp:   time.Now(),
	}
	return s.sentMessage, nil
}

func (s *channelRepoStub) ToggleMessageReaction(_ context.Context, _, _, _, emoji string) ([]domain.CommunityChatReaction, error) {
	s.toggledEmoji = emoji
	return []domain.CommunityChatReaction{{Emoji: emoji, Count: 1, Users: []string{"u1"}}}, nil
}

func (s *channelRepoStub) GetCommunityMembers(_ context.Context, _ string, _, _ int, _ string) ([]domain.CommunityMember, int, error) {
	return s.members, len(s.members), nil
}

func (s *channelRepoStub) KickCommunityMember(_ context.Context, _, targetUserID string) (int, error) {
	s.kickedUser = targetUserID
	return 10, nil
}

func (s *channelRepoStub) GetPromotedEvents(_ context.Context, _ string) ([]domain.Event, error) {
	return s.promotedEvents, nil
}

func (s *channelRepoStub) UpdatePromotedEvents(_ context.Context, _ string, eventIDs []string) ([]domain.Event, error) {
	s.updatedEvents = eventIDs
	return s.promotedEvents, nil
}

func TestChatSettingsModeValidation(t *testing.T) {
	repo := &channelRepoStub{
		community: &domain.Community{ID: "c1", CanPublish: true},
	}
	svc := NewCommunityService(repo)

	// Valid broadcast_only
	_, err := svc.UpdateChatSettings(context.Background(), "c1", "user-admin", "broadcast_only")
	if err != nil {
		t.Fatalf("unexpected error for broadcast_only: %v", err)
	}

	// Valid open_chat
	_, err = svc.UpdateChatSettings(context.Background(), "c1", "user-admin", "open_chat")
	if err != nil {
		t.Fatalf("unexpected error for open_chat: %v", err)
	}

	// Invalid mode
	_, err = svc.UpdateChatSettings(context.Background(), "c1", "user-admin", "invalid_mode")
	if !errors.Is(err, domain.ErrCommunityInvalid) {
		t.Fatalf("expected ErrCommunityInvalid, got %v", err)
	}

	// Non-manager receives forbidden
	repo.community.CanPublish = false
	_, err = svc.UpdateChatSettings(context.Background(), "c1", "user-member", "open_chat")
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected ErrCommunityForbidden, got %v", err)
	}
}

func TestCommunityMessagesPartialPrivacy(t *testing.T) {
	repo := &channelRepoStub{
		community: &domain.Community{ID: "c1", IsMember: false, CanPublish: false},
		messages:  []domain.CommunityChatMessage{{ID: "m1", Content: "Hello"}},
	}
	svc := NewCommunityService(repo)

	// Guest / Non-member is blocked from reading messages
	_, err := svc.GetCommunityMessages(context.Background(), "c1", "guest-user", 50, "")
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected ErrCommunityForbidden for non-member, got %v", err)
	}

	// Member can read
	repo.community.IsMember = true
	msgs, err := svc.GetCommunityMessages(context.Background(), "c1", "member-user", 50, "")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("member should be able to read messages: %v", err)
	}
}

func TestSendCommunityMessageModeEnforcement(t *testing.T) {
	repo := &channelRepoStub{
		community:    &domain.Community{ID: "c1", IsMember: true, CanPublish: false},
		chatSettings: &domain.CommunityChatSettings{CommunityID: "c1", Mode: "broadcast_only"},
	}
	svc := NewCommunityService(repo)

	// Ordinary member sending in broadcast_only mode -> blocked
	_, err := svc.SendCommunityMessage(context.Background(), "c1", "member-1", "Hola a todos")
	if !errors.Is(err, domain.ErrCommunityChannelClosed) {
		t.Fatalf("expected ErrCommunityChannelClosed, got %v", err)
	}

	// RRPP / Publisher sending in broadcast_only mode -> allowed
	repo.community.CanPublish = true
	msg, err := svc.SendCommunityMessage(context.Background(), "c1", "rrpp-1", "Comunicado oficial")
	if err != nil || msg == nil {
		t.Fatalf("rrpp should be able to send in broadcast_only: %v", err)
	}

	// Switch to open_chat -> ordinary member can send
	repo.chatSettings.Mode = "open_chat"
	repo.community.CanPublish = false
	repo.community.IsMember = true
	msg, err = svc.SendCommunityMessage(context.Background(), "c1", "member-1", "Comentario de miembro")
	if err != nil || msg == nil {
		t.Fatalf("member should be able to send in open_chat: %v", err)
	}
}

func TestToggleMessageReactionPermissions(t *testing.T) {
	repo := &channelRepoStub{
		community: &domain.Community{ID: "c1", IsMember: false, CanPublish: false},
	}
	svc := NewCommunityService(repo)

	// Non-member cannot react
	_, err := svc.ToggleMessageReaction(context.Background(), "c1", "00000000-0000-0000-0000-000000000001", "guest", "🔥")
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected ErrCommunityForbidden, got %v", err)
	}

	// Invalid UUID
	repo.community.IsMember = true
	_, err = svc.ToggleMessageReaction(context.Background(), "c1", "not-a-uuid", "member", "🔥")
	if !errors.Is(err, domain.ErrCommunityInvalid) {
		t.Fatalf("expected ErrCommunityInvalid for bad uuid, got %v", err)
	}

	// Valid reaction by member
	reactions, err := svc.ToggleMessageReaction(context.Background(), "c1", "00000000-0000-0000-0000-000000000001", "member", "🔥")
	if err != nil || len(reactions) == 0 {
		t.Fatalf("unexpected error toggling reaction: %v", err)
	}
}

func TestMembersDirectorySocialProof(t *testing.T) {
	repo := &channelRepoStub{
		community: &domain.Community{ID: "c1", IsMember: false, CanPublish: false},
		members: []domain.CommunityMember{
			{ID: "u1", Name: "Alex RRPP", Role: "RRPP"},
			{ID: "u2", Name: "Franco Miembro", Role: "MEMBER"},
		},
	}
	svc := NewCommunityService(repo)

	// Guests CAN see members for social proof!
	members, total, err := svc.GetCommunityMembers(context.Background(), "c1", 20, 0, "")
	if err != nil {
		t.Fatalf("guests should be able to view members: %v", err)
	}
	if total != 2 || len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
}

func TestPromotedEventsPartialPrivacy(t *testing.T) {
	repo := &channelRepoStub{
		community:      &domain.Community{ID: "c1", IsMember: false, CanPublish: false},
		promotedEvents: []domain.Event{{ID: "evt-1", Title: "Afterlife"}},
	}
	svc := NewCommunityService(repo)

	// Non-member cannot see promoted events
	_, err := svc.GetPromotedEvents(context.Background(), "c1", "guest")
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected ErrCommunityForbidden for non-member, got %v", err)
	}

	// Member can see
	repo.community.IsMember = true
	events, err := svc.GetPromotedEvents(context.Background(), "c1", "member")
	if err != nil || len(events) != 1 {
		t.Fatalf("member should be able to view promoted events: %v", err)
	}

	// Non-manager cannot update promoted events
	_, err = svc.UpdatePromotedEvents(context.Background(), "c1", "member", []string{"00000000-0000-0000-0000-000000000001"})
	if !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("expected ErrCommunityForbidden on update by non-manager, got %v", err)
	}

	// Manager can update
	repo.community.CanPublish = true
	updated, err := svc.UpdatePromotedEvents(context.Background(), "c1", "rrpp", []string{"00000000-0000-0000-0000-000000000001"})
	if err != nil || len(updated) != 1 {
		t.Fatalf("manager should be able to update promoted events: %v", err)
	}
}
