package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
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
	SendMessage(ctx context.Context, chatID, currentUserID, content string, msgType domain.MessageType) (*domain.Message, error)
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
}

type EventService interface {
	GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error)
	GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error)
	GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error)
	RSVPEvent(ctx context.Context, userID, eventID, status string) (goingCount int, notGoingCount int, userRsvp string, err error)
	GetFollowedGoingAttendees(ctx context.Context, eventID string, currentUserID string, limit, offset int) ([]domain.Attendee, int, error)
	GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error)
	CreateEventComment(ctx context.Context, eventID string, authorID string, content string) (*domain.EventComment, error)
}
