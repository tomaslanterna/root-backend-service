package ports

import (
	"context"
	"root-backend-service/internal/core/domain"
)

type UserRepository interface {
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetUserByUsername(ctx context.Context, username string) (*domain.User, error)
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
	CreateUser(ctx context.Context, user *domain.User) error
	AddFollower(ctx context.Context, followerID, followedID string) error
	RemoveFollower(ctx context.Context, followerID, followedID string) error
	SearchUsers(ctx context.Context, query string, currentUserID string) ([]domain.User, error)
	UpdateUser(ctx context.Context, user *domain.User) error
	UpdateUserKycStatus(ctx context.Context, userID string, isVerified bool, country string) error
}

type KycRepository interface {
	CreateSession(ctx context.Context, session *domain.KycSession) error
	GetSessionByID(ctx context.Context, id string) (*domain.KycSession, error)
	UpdateSession(ctx context.Context, session *domain.KycSession) error
	GetLastSessionByUserID(ctx context.Context, userID string) (*domain.KycSession, error)
}

type ChatRepository interface {
	CreateChat(ctx context.Context, chat *domain.Chat) error
	GetChatByID(ctx context.Context, id string) (*domain.Chat, error)
	UpdateLastMessage(ctx context.Context, chatID string, lastMessage string) error
	AddParticipant(ctx context.Context, participant *domain.ChatParticipant) error
	GetUserChats(ctx context.Context, userID string) ([]domain.Chat, error)
	GetDirectChatBetweenUsers(ctx context.Context, user1ID, user2ID string) (*domain.Chat, error)
}

type MessageRepository interface {
	CreateMessage(ctx context.Context, msg *domain.Message) error
	GetMessagesByChatID(ctx context.Context, chatID string, afterTimestamp string) ([]domain.Message, error)
}

type TransferRepository interface {
	CreateTransfer(ctx context.Context, transfer *domain.Transfer) error
	GetTransferByID(ctx context.Context, id string) (*domain.Transfer, error)
	UpdateStatus(ctx context.Context, transferID string, status domain.TransferStatus, ticketURL *string) error
	UpdateStartDeal(ctx context.Context, transferID, buyerID, chatID string) error
	GetTransfers(ctx context.Context, status *string) ([]domain.Transfer, error)
}

type EventRepository interface {
	GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error)
	GetEvents(ctx context.Context, featuredOnly *bool, country string) ([]domain.Event, error)
	GetEventByID(ctx context.Context, id string) (*domain.Event, error)
	RSVPEvent(ctx context.Context, userID, eventID, status string) (goingCount int, notGoingCount int, err error)
	SearchEvents(ctx context.Context, query string) ([]domain.Event, error)
}
