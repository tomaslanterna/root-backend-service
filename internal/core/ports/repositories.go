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
	InitSchema(ctx context.Context) error
}

type EventRepository interface {
	GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error)
	GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error)
	GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error)
	RSVPEvent(ctx context.Context, userID, eventID, status string) (goingCount int, notGoingCount int, userRsvp string, err error)
	SearchEvents(ctx context.Context, query string) ([]domain.Event, error)
	GetFollowedGoingAttendees(ctx context.Context, eventID string, currentUserID string, limit, offset int) ([]domain.Attendee, int, error)
	GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error)
	CreateEventComment(ctx context.Context, eventID string, authorID string, content string) (*domain.EventComment, error)
	InitSchema(ctx context.Context) error
}
