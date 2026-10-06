package ports

import (
	"context"
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
	FollowUser(ctx context.Context, currentUserID, targetUsername string) error
	UnfollowUser(ctx context.Context, currentUserID, targetUsername string) error
	UpdateUser(ctx context.Context, userID, newUsername, dob, documentID, country string) (*domain.User, error)
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
	GetLiveEventStatus(ctx context.Context, userID string, lat, lng float64) (*domain.Event, error)
}

type FeedData struct {
	Data       []domain.Post          `json:"data"`
	Pagination map[string]interface{} `json:"pagination"`
}

type PostService interface {
	GetFeeds(ctx context.Context, userID string, includeFeeds []string, pagination map[string]int) (map[string]FeedData, error)
	GetPostByID(ctx context.Context, id string, currentUserID string) (*domain.Post, error)
	CreatePost(ctx context.Context, post *domain.Post) error
	GetCommunityAnnouncements(ctx context.Context, communityID string, limit, offset int) ([]domain.Post, int, error)
	CreateCommunityAnnouncement(ctx context.Context, communityID, authorID string, post *domain.Post) (*domain.Post, error)
	GetPostComments(ctx context.Context, postID string, limit, offset int) ([]domain.EventComment, int, error)
	CreatePostComment(ctx context.Context, postID string, authorID string, content string) (*domain.EventComment, error)
}

type CommunityService interface {
	GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error)
	GetCommunity(ctx context.Context, identifier string, currentUserID string) (*domain.Community, error)
	JoinCommunity(ctx context.Context, communityID, userID string) (membersCount int, err error)
	LeaveCommunity(ctx context.Context, communityID, userID string) (membersCount int, err error)
	GetUserCommunities(ctx context.Context, username, currentUserID string) ([]domain.Community, error)
	GetCurrentUserCommunities(ctx context.Context, userID string) ([]domain.Community, error)
}

type SyncDanceRequest struct {
	EventID    string    `json:"eventId"`
	StepsCount int       `json:"stepsCount"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
}

type DanceService interface {
	SyncSteps(ctx context.Context, userID string, req SyncDanceRequest) (*domain.DanceSession, error)
	GetCrewLeaderboards(ctx context.Context, squadID string, eventID *string) (map[string]interface{}, error)
	GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error)
	GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error)
	GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error)
	GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error)
}

type TicketInfoResult struct {
	Source      *string             `json:"source"`
	TicketTiers []domain.TicketTier `json:"ticketTiers"`
}

type AiTicketService interface {
	FetchEventTickets(ctx context.Context, title, date, location, country, ticketURL string) (*TicketInfoResult, error)
}
