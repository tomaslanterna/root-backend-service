package ports

import (
	"errors"
	"context"
	"io"
	"root-backend-service/internal/core/domain"
	"time"
)

type AuthService interface {
	Login(ctx context.Context, email, password string) (string, *domain.User, error)
	Register(ctx context.Context, name, username, email, password, role string, dob *string, documentID *string, country *string) (*domain.User, error)
	GoogleLogin(ctx context.Context, idToken string) (string, *domain.User, error)
}

type UserService interface {
	GetUserByUsername(ctx context.Context, targetUsername string, currentUserID string) (*domain.User, bool, error)
	// FollowUser sigue al instante o, si el perfil es privado, manda una solicitud. Devuelve el estado final.
	FollowUser(ctx context.Context, currentUserID, targetUsername string) (string, error)
	FollowStatus(ctx context.Context, viewerID string, target *domain.User) (string, error)
	CanViewProfile(ctx context.Context, viewerID, targetUsername string) (*domain.User, bool, error)
	SetPrivacy(ctx context.Context, userID string, isPrivate bool) error
	ListFollowRequests(ctx context.Context, userID string) ([]domain.FollowListUser, error)
	AcceptFollowRequest(ctx context.Context, userID, requesterUsername string) error
	RejectFollowRequest(ctx context.Context, userID, requesterUsername string) error
	UnfollowUser(ctx context.Context, currentUserID, targetUsername string) error
	GetFollowers(ctx context.Context, targetUsername, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error)
	GetFollowing(ctx context.Context, targetUsername, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error)
	UpdateUser(ctx context.Context, userID, name, newUsername, dob, documentID, country string) (*domain.User, error)
	UpdateAvatar(ctx context.Context, userID, avatar string) (*domain.User, error)
	GetInterests(ctx context.Context, userID string) (genres []string, answered bool, err error)
	SetInterests(ctx context.Context, userID string, genres []string) ([]string, error)
}

type SearchService interface {
	Search(ctx context.Context, query, searchType, country, currentUserID string) (interface{}, error)
}

type ChatService interface {
	GetMessages(ctx context.Context, chatID string, afterTimestamp string, currentUserID string) ([]domain.Message, error)
	SendMessage(ctx context.Context, chatID, currentUserID, content string, msgType domain.MessageType, clientID ...string) (*domain.Message, error)
	AcknowledgeMessages(ctx context.Context, chatID, userID string, messageIDs []string, read bool) error
	MarkMessagesRead(ctx context.Context, chatID, currentUserID string) error
	GetUserChats(ctx context.Context, userID string) ([]domain.Chat, error)
	GetOrCreateDirectChat(ctx context.Context, currentUserID, targetUserID string) (*domain.Chat, error)
	GetChatByID(ctx context.Context, chatID, currentUserID string) (*domain.Chat, error)
}

type TransferService interface {
	CreateTransfer(ctx context.Context, eventID, sellerID string, price float64) (*domain.Transfer, error)
	GetTransfer(ctx context.Context, transferID, currentUserID string) (*domain.Transfer, error)
	UpdateTransferStatus(ctx context.Context, transferID, currentUserID string, status domain.TransferStatus, ticketURL *string) error
	StartDeal(ctx context.Context, transferID, buyerID string) error
	GetTransfers(ctx context.Context, status *string) ([]domain.Transfer, error)
	CreatePaymentPreference(ctx context.Context, transferID, buyerID string) (string, error)
	HandleMercadoPagoWebhook(ctx context.Context, topic, id string) error
}

type EventService interface {
	GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error)
	GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error)
	GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error)
	RSVPEvent(ctx context.Context, userID, eventID, status string) (goingCount int, notGoingCount int, userRsvp string, err error)
	GetFollowedGoingAttendees(ctx context.Context, eventID string, currentUserID string, limit, offset int) ([]domain.Attendee, int, error)
	GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error)
	CreateEventComment(ctx context.Context, eventID string, authorID string, content string) (*domain.EventComment, error)
	GetPendingSurveys(ctx context.Context, userID string) ([]domain.Event, error)
	GetUserEvents(ctx context.Context, username string) ([]domain.Event, error)
	GetLiveEventStatus(ctx context.Context, userID string, lat, lng float64) (*domain.Event, int, error)
	BulkCreateFromCSV(ctx context.Context, file io.Reader) (int, error)
	UpdateEventBanner(ctx context.Context, id string, banner string) error
}

type FeedData struct {
	Data       []domain.Post          `json:"data"`
	Pagination map[string]interface{} `json:"pagination"`
}

type PostService interface {
	GetFeeds(ctx context.Context, userID string, includeFeeds []string, pagination map[string]int) (map[string]FeedData, error)
	GetPostByID(ctx context.Context, id string, currentUserID string) (*domain.Post, error)
	GetUserPosts(ctx context.Context, authorID string, limit, offset int) ([]domain.Post, error)
	CreatePost(ctx context.Context, post *domain.Post) error
	GetCommunityAnnouncements(ctx context.Context, communityID string, limit, offset int) ([]domain.Post, int, error)
	CreateCommunityAnnouncement(ctx context.Context, communityID, authorID string, post *domain.Post) (*domain.Post, error)
	GetPostComments(ctx context.Context, postID string, limit, offset int) ([]domain.EventComment, int, error)
	CreatePostComment(ctx context.Context, postID string, authorID string, content string) (*domain.EventComment, error)
	ToggleLike(ctx context.Context, postID, userID string) (liked bool, count int, err error)
}

type CommunityService interface {
	SetMuted(ctx context.Context, communityID, userID string, muted bool) error
	MarkRead(ctx context.Context, communityID, userID, postID string) error
	SetPinned(ctx context.Context, communityID, userID, postID string, pinned bool) error
	CreateReport(ctx context.Context, communityID, userID string, input domain.CommunityReportInput) (string, error)
	GetReports(ctx context.Context, communityID, userID string, limit, offset int) ([]domain.CommunityReport, int, error)
	ReviewReport(ctx context.Context, communityID, userID, reportID, status string) error
	GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error)
	GetCommunity(ctx context.Context, identifier string, currentUserID string) (*domain.Community, error)
	JoinCommunity(ctx context.Context, communityID, userID string) (membersCount int, err error)
	LeaveCommunity(ctx context.Context, communityID, userID string) (membersCount int, err error)
	GetUserCommunities(ctx context.Context, username, currentUserID string) ([]domain.Community, error)
	GetCurrentUserCommunities(ctx context.Context, userID string) ([]domain.Community, error)
	GetChatSettings(ctx context.Context, communityID string) (*domain.CommunityChatSettings, error)
	UpdateChatSettings(ctx context.Context, communityID, userID, mode string) (*domain.CommunityChatSettings, error)
	GetCommunityMessages(ctx context.Context, communityID, userID string, limit int, before string) ([]domain.CommunityChatMessage, error)
	SendCommunityMessage(ctx context.Context, communityID, userID, content string) (*domain.CommunityChatMessage, error)
	ToggleMessageReaction(ctx context.Context, communityID, messageID, userID, emoji string) ([]domain.CommunityChatReaction, error)
	GetCommunityMembers(ctx context.Context, communityID string, limit, offset int, query string) ([]domain.CommunityMember, int, error)
	KickCommunityMember(ctx context.Context, communityID, actorUserID, targetUserID string) (remainingCount int, err error)
	GetPromotedEvents(ctx context.Context, communityID, userID string) ([]domain.Event, error)
	UpdatePromotedEvents(ctx context.Context, communityID, userID string, eventIDs []string) ([]domain.Event, error)
}

type SyncDanceRequest struct {
	EventID    string    `json:"eventId"`
	StepsCount int       `json:"stepsCount"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
}

// ErrNotCrewHost: la acción es solo para quien creó la crew.
var ErrNotCrewHost = errors.New("only the crew host can do this")

type CreateCrewRequest struct {
	Name      string   `json:"name"`
	// IsPublic: las públicas aparecen en el listado de Crews y cualquiera se une sin código;
	// las privadas solo con el link de invitación.
	IsPublic  bool     `json:"is_public"`
	Type      string   `json:"type"`
	MemberIDs []string `json:"member_ids"`
	EventID   *string  `json:"event_id,omitempty"`
	EventIDs  []string `json:"event_ids,omitempty"`
}

type DanceService interface {
	SyncSteps(ctx context.Context, userID string, req SyncDanceRequest) (*domain.DanceSession, error)
	CreatePermanentCrew(ctx context.Context, creatorID string, req CreateCrewRequest) (map[string]interface{}, error)
	JoinPermanentCrew(ctx context.Context, userID, codeOrID string) (map[string]interface{}, error)
	JoinPublicCrew(ctx context.Context, userID, squadID string) (map[string]interface{}, error)
	SetCrewVisibility(ctx context.Context, squadID, userID string, isPublic bool) error
	AddCrewMembers(ctx context.Context, squadID string, memberIDs []string, actorUserID string) (map[string]interface{}, error)
	AddCrewEvent(ctx context.Context, squadID, eventID, userID string) error
	GetCrewLeaderboards(ctx context.Context, squadID string, eventID *string) (map[string]interface{}, error)
	GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error)
	GetAllCrews(ctx context.Context) ([]map[string]interface{}, error)
	GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error)
	GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error)
	IsCrewMember(ctx context.Context, squadID, userID string) (bool, error)
	RegenerateCrewInviteCode(ctx context.Context, squadID, userID string) (string, error)
	GetCrewInvitePreview(ctx context.Context, inviteCode, viewerID string) (map[string]interface{}, error)
	GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error)
	GetEventLiveRanking(ctx context.Context, eventID string) (map[string]interface{}, error)
}

type TicketInfoResult struct {
	Source      *string             `json:"source"`
	TicketTiers []domain.TicketTier `json:"ticketTiers"`
}

type AiTicketService interface {
	FetchEventTickets(ctx context.Context, title, date, location, country, ticketURL string) (*TicketInfoResult, error)
}

type ContentModerationService interface {
	ValidateContent(ctx context.Context, content string) error
}
