package auth

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"testing"
)

type loginTestRepo struct {
	ports.UserRepository
	user domain.User
}

func (r *loginTestRepo) GetUserByEmail(context.Context, string) (*domain.User, error) {
	u := r.user
	return &u, nil
}

type loginTestSessions struct {
	hash    string
	version int64
	err     error
}

func (s *loginTestSessions) GetPasswordAndSessionVersion(context.Context, string) (string, int64, error) {
	return s.hash, s.version, s.err
}
func (s *loginTestSessions) GetSessionVersion(context.Context, string) (int64, error) {
	return s.version, s.err
}
func TestLoginAfterResetRejectsOldPasswordAndUsesFreshSessionVersion(t *testing.T) {
	t.Setenv("JWT_SECRET", "auth-test-secret")
	oldHash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.MinCost)
	newHash, _ := bcrypt.GenerateFromPassword([]byte("NewPassword123"), bcrypt.MinCost)
	repo := &loginTestRepo{user: domain.User{ID: "user", Role: "USER", PasswordHash: string(oldHash)}}
	sessions := &loginTestSessions{hash: string(newHash), version: 1}
	svc := NewAuthService(repo, sessions)
	if _, _, err := svc.Login(context.Background(), "person@example.com", "OldPassword123"); err == nil {
		t.Fatal("stale password snapshot authorized a fresh session")
	}
	token, _, err := svc.Login(context.Background(), "person@example.com", "NewPassword123")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return []byte("auth-test-secret"), nil })
	if err != nil || parsed.Claims.(jwt.MapClaims)["sessionVersion"] != float64(1) {
		t.Fatal("fresh JWT lacks matching persisted version")
	}
	if !errors.Is(svc.ValidateSession(context.Background(), "user", 0), domain.ErrInvalidSession) {
		t.Fatal("old session accepted")
	}
	if svc.ValidateSession(context.Background(), "user", 1) != nil {
		t.Fatal("new session rejected")
	}
	sessions.err = errors.New("database unavailable")
	if !errors.Is(svc.ValidateSession(context.Background(), "user", 1), domain.ErrInvalidSession) {
		t.Fatal("session validator must fail closed")
	}
}
