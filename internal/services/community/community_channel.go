package community

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
)

func (s *communityService) GetChatSettings(ctx context.Context, communityID string) (*domain.CommunityChatSettings, error) {
	c, err := s.GetCommunity(ctx, communityID, "")
	if err != nil {
		return nil, err
	}
	return s.repo.GetChatSettings(ctx, c.ID)
}

func (s *communityService) UpdateChatSettings(ctx context.Context, communityID, userID, mode string) (*domain.CommunityChatSettings, error) {
	mode = strings.TrimSpace(mode)
	if mode != "broadcast_only" && mode != "open_chat" {
		return nil, domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, communityID, userID, true, false)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateChatSettings(ctx, c.ID, userID, mode)
}

func (s *communityService) GetCommunityMessages(ctx context.Context, communityID, userID string, limit int, before string) ([]domain.CommunityChatMessage, error) {
	c, err := s.GetCommunity(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	// Partial privacy: Non-members cannot see channel messages
	if !c.IsMember && !c.CanPublish {
		return nil, domain.ErrCommunityForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.GetCommunityMessages(ctx, c.ID, limit, before)
}

func (s *communityService) SendCommunityMessage(ctx context.Context, communityID, userID, content string) (*domain.CommunityChatMessage, error) {
	if userID == "" {
		return nil, domain.ErrCommunityForbidden
	}
	content = strings.TrimSpace(content)
	runeCount := utf8.RuneCountInString(content)
	if runeCount == 0 || runeCount > 4000 {
		return nil, domain.ErrCommunityInvalid
	}

	c, err := s.GetCommunity(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}

	settings, err := s.repo.GetChatSettings(ctx, c.ID)
	if err != nil {
		return nil, err
	}

	if settings.Mode == "broadcast_only" {
		if !c.CanPublish {
			return nil, domain.ErrCommunityChannelClosed
		}
	} else {
		// open_chat: member or publisher can post
		if !c.IsMember && !c.CanPublish {
			return nil, domain.ErrCommunityForbidden
		}
	}

	return s.repo.SendCommunityMessage(ctx, c.ID, userID, content)
}

func (s *communityService) ToggleMessageReaction(ctx context.Context, communityID, messageID, userID, emoji string) ([]domain.CommunityChatReaction, error) {
	if userID == "" {
		return nil, domain.ErrCommunityForbidden
	}
	if _, err := uuid.Parse(messageID); err != nil {
		return nil, domain.ErrCommunityInvalid
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || utf8.RuneCountInString(emoji) > 16 {
		return nil, domain.ErrCommunityInvalid
	}

	c, err := s.GetCommunity(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if !c.IsMember && !c.CanPublish {
		return nil, domain.ErrCommunityForbidden
	}

	return s.repo.ToggleMessageReaction(ctx, c.ID, messageID, userID, emoji)
}

func (s *communityService) GetCommunityMembers(ctx context.Context, communityID string, limit, offset int, query string) ([]domain.CommunityMember, int, error) {
	c, err := s.GetCommunity(ctx, communityID, "")
	if err != nil {
		return nil, 0, err
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.GetCommunityMembers(ctx, c.ID, limit, offset, query)
}

func (s *communityService) KickCommunityMember(ctx context.Context, communityID, actorUserID, targetUserID string) (int, error) {
	if _, err := uuid.Parse(targetUserID); err != nil {
		return 0, domain.ErrCommunityInvalid
	}
	if actorUserID == targetUserID {
		return 0, domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, communityID, actorUserID, true, false)
	if err != nil {
		return 0, err
	}
	return s.repo.KickCommunityMember(ctx, c.ID, targetUserID)
}

func (s *communityService) GetPromotedEvents(ctx context.Context, communityID, userID string) ([]domain.Event, error) {
	c, err := s.GetCommunity(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	// Partial privacy: Non-members cannot view community promoted events
	if !c.IsMember && !c.CanPublish {
		return nil, domain.ErrCommunityForbidden
	}
	return s.repo.GetPromotedEvents(ctx, c.ID)
}

func (s *communityService) UpdatePromotedEvents(ctx context.Context, communityID, userID string, eventIDs []string) ([]domain.Event, error) {
	c, err := s.resolve(ctx, communityID, userID, true, false)
	if err != nil {
		return nil, err
	}
	for _, id := range eventIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, domain.ErrCommunityInvalid
		}
	}
	return s.repo.UpdatePromotedEvents(ctx, c.ID, eventIDs)
}
