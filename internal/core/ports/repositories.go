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
