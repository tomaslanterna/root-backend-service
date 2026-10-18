package search

import (
	"context"
	"testing"
	"root-backend-service/internal/core/domain"
)

type mockCommunityRepo struct {
	getCommunitiesCalled bool
	lastQuery            string
	communities          []domain.Community
}

func (m *mockCommunityRepo) SetMuted(ctx context.Context, communityID, userID string, muted bool) error {
	return nil
}
func (m *mockCommunityRepo) MarkRead(ctx context.Context, communityID, userID, postID string) error {
	return nil
}
func (m *mockCommunityRepo) SetPinned(ctx context.Context, communityID, postID string, pinned bool) error {
	return nil
}
func (m *mockCommunityRepo) CreateReport(ctx context.Context, communityID, userID string, input domain.CommunityReportInput) (string, error) {
	return "", nil
}
func (m *mockCommunityRepo) GetReports(ctx context.Context, communityID string, limit, offset int) ([]domain.CommunityReport, int, error) {
	return nil, 0, nil
}
func (m *mockCommunityRepo) ReviewReport(ctx context.Context, communityID, userID, reportID, status string) error {
	return nil
}
func (m *mockCommunityRepo) InitSchema(ctx context.Context) error {
	return nil
}
func (m *mockCommunityRepo) GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	m.getCommunitiesCalled = true
	m.lastQuery = filter.Query
	return m.communities, len(m.communities), nil
}
func (m *mockCommunityRepo) GetCommunityByIDOrSlug(ctx context.Context, identifier string, currentUserID string) (*domain.Community, error) {
	return nil, nil
}
func (m *mockCommunityRepo) JoinCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return 0, nil
}
func (m *mockCommunityRepo) LeaveCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return 0, nil
}
func (m *mockCommunityRepo) GetUserCommunities(ctx context.Context, username, currentUserID string) ([]domain.Community, error) {
	return nil, nil
}
func (m *mockCommunityRepo) GetCurrentUserCommunities(ctx context.Context, userID string) ([]domain.Community, error) {
	return nil, nil
}
func (m *mockCommunityRepo) CanPublish(ctx context.Context, communityID, userID string) (bool, bool, error) {
	return false, false, nil
}
func (m *mockCommunityRepo) GetChatSettings(ctx context.Context, communityID string) (*domain.CommunityChatSettings, error) {
	return nil, nil
}
func (m *mockCommunityRepo) UpdateChatSettings(ctx context.Context, communityID, userID, mode string) (*domain.CommunityChatSettings, error) {
	return nil, nil
}
func (m *mockCommunityRepo) GetCommunityMessages(ctx context.Context, communityID string, limit int, before string) ([]domain.CommunityChatMessage, error) {
	return nil, nil
}
func (m *mockCommunityRepo) SendCommunityMessage(ctx context.Context, communityID, senderID, content string) (*domain.CommunityChatMessage, error) {
	return nil, nil
}
func (m *mockCommunityRepo) ToggleMessageReaction(ctx context.Context, communityID, messageID, userID, emoji string) ([]domain.CommunityChatReaction, error) {
	return nil, nil
}
func (m *mockCommunityRepo) GetCommunityMembers(ctx context.Context, communityID string, limit, offset int, query string) ([]domain.CommunityMember, int, error) {
	return nil, 0, nil
}
func (m *mockCommunityRepo) KickCommunityMember(ctx context.Context, communityID, targetUserID string) (int, error) {
	return 0, nil
}
func (m *mockCommunityRepo) GetPromotedEvents(ctx context.Context, communityID string) ([]domain.Event, error) {
	return nil, nil
}
func (m *mockCommunityRepo) UpdatePromotedEvents(ctx context.Context, communityID string, eventIDs []string) ([]domain.Event, error) {
	return nil, nil
}
func (m *mockCommunityRepo) GetActiveCommunityMemberIDs(ctx context.Context, communityID string) ([]string, error) {
	return nil, nil
}

func TestSearchCommunity(t *testing.T) {
	mockRepo := &mockCommunityRepo{
		communities: []domain.Community{
			{
				ID:   "comm-1",
				Name: "Techno Resistance",
				Slug: "techno-resistance",
			},
		},
	}

	service := NewSearchService(nil, nil, mockRepo)

	// Test COMMUNITY
	results, err := service.Search(context.Background(), "techno", "COMMUNITY", "AR", "user-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockRepo.getCommunitiesCalled {
		t.Fatalf("expected GetCommunities to be called")
	}

	if mockRepo.lastQuery != "techno" {
		t.Fatalf("expected query to be 'techno', got %s", mockRepo.lastQuery)
	}

	comms, ok := results.([]domain.Community)
	if !ok || len(comms) != 1 || comms[0].ID != "comm-1" {
		t.Fatalf("unexpected results: %+v", results)
	}

	// Test COMMUNITIES
	resultsPlural, err := service.Search(context.Background(), "techno", "COMMUNITIES", "AR", "user-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	commsPlural, ok := resultsPlural.([]domain.Community)
	if !ok || len(commsPlural) != 1 {
		t.Fatalf("unexpected results for COMMUNITIES: %+v", resultsPlural)
	}
}
