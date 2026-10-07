package community

import (
	"context"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"strings"
	"unicode/utf8"
)

func (s *communityService) resolve(ctx context.Context, identifier, userID string, manager, member bool) (*domain.Community, error) {
	c, err := s.GetCommunity(ctx, identifier, userID)
	if err != nil {
		return nil, err
	}
	if userID == "" || (manager && !c.CanPublish) || (member && !c.IsMember) {
		return nil, domain.ErrCommunityForbidden
	}
	return c, nil
}

func (s *communityService) SetMuted(ctx context.Context, id, userID string, muted bool) error {
	c, err := s.resolve(ctx, id, userID, false, true)
	if err != nil {
		return err
	}
	return s.repo.SetMuted(ctx, c.ID, userID, muted)
}

func (s *communityService) MarkRead(ctx context.Context, id, userID, postID string) error {
	postUUID, err := uuid.Parse(postID)
	if err != nil {
		return domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, id, userID, false, true)
	if err != nil {
		return err
	}
	return s.repo.MarkRead(ctx, c.ID, userID, postUUID.String())
}

func (s *communityService) SetPinned(ctx context.Context, id, userID, postID string, pinned bool) error {
	postUUID, err := uuid.Parse(postID)
	if err != nil {
		return domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, id, userID, true, false)
	if err != nil {
		return err
	}
	return s.repo.SetPinned(ctx, c.ID, postUUID.String(), pinned)
}

func (s *communityService) CreateReport(ctx context.Context, id, userID string, input domain.CommunityReportInput) (string, error) {
	input.Details = strings.TrimSpace(input.Details)
	targetUUID, err := uuid.Parse(input.TargetID)
	if err != nil {
		return "", domain.ErrCommunityInvalid
	}
	input.TargetID = targetUUID.String()
	if (input.TargetType != "post" && input.TargetType != "comment") || (input.Reason != "spam" && input.Reason != "abuse" && input.Reason != "other") || utf8.RuneCountInString(input.Details) > 1000 {
		return "", domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, id, userID, false, false)
	if err != nil {
		return "", err
	}
	return s.repo.CreateReport(ctx, c.ID, userID, input)
}

func (s *communityService) GetReports(ctx context.Context, id, userID string, limit, offset int) ([]domain.CommunityReport, int, error) {
	if limit < 1 || limit > 50 || offset < 0 {
		return nil, 0, domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, id, userID, true, false)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.GetReports(ctx, c.ID, limit, offset)
}

func (s *communityService) ReviewReport(ctx context.Context, id, userID, reportID, status string) error {
	reportUUID, err := uuid.Parse(reportID)
	if err != nil {
		return domain.ErrCommunityInvalid
	}
	if status != "reviewed" && status != "dismissed" {
		return domain.ErrCommunityInvalid
	}
	c, err := s.resolve(ctx, id, userID, true, false)
	if err != nil {
		return err
	}
	return s.repo.ReviewReport(ctx, c.ID, userID, reportUUID.String(), status)
}
