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

type EventService interface {
	GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error)
	GetEvents(ctx context.Context, featuredOnly *bool, country string) ([]domain.Event, error)
	GetEventByID(ctx context.Context, id string) (*domain.Event, error)
	RSVPEvent(ctx context.Context, userID, eventID, status string) (goingCount int, notGoingCount int, err error)
}

