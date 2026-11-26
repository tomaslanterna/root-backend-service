package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type registerTestRepo struct {
	ports.UserRepository
	created *domain.User
}

func (r *registerTestRepo) GetUserByEmail(context.Context, string) (*domain.User, error) {
	return nil, errors.New("not found")
}

func (r *registerTestRepo) CreateUser(_ context.Context, user *domain.User) error {
	r.created = user
	return nil
}

func TestRegisterAlwaysCreatesUsersWithTerms(t *testing.T) {
	repo := &registerTestRepo{}
	svc := NewAuthService(repo)
	if _, err := svc.Register(context.Background(), "Ana Paz", "ana", "ana@example.com", "Password123", domain.CurrentTermsVersion, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	u := repo.created
	if u == nil || u.Role != "USER" || u.TermsAcceptedAt == nil || u.TermsVersion == nil || *u.TermsVersion != domain.CurrentTermsVersion {
		t.Fatalf("created %+v", u)
	}
	if _, err := svc.Register(context.Background(), "Ana Paz", "ana", "ana@example.com", "Password123", "", nil, nil, nil); !errors.Is(err, domain.ErrTermsNotAccepted) {
		t.Fatalf("register without terms err = %v", err)
	}
}

func TestLoginRejectsDeletedAndSuspendedAccounts(t *testing.T) {
	t.Setenv("JWT_SECRET", "auth-test-secret")
	hash, _ := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	now := time.Now()
	for name, user := range map[string]domain.User{
		"deleted":   {ID: "u", Role: "USER", PasswordHash: string(hash), DeletedAt: &now},
		"suspended": {ID: "u", Role: "USER", PasswordHash: string(hash), SuspendedAt: &now},
	} {
		svc := NewAuthService(&loginTestRepo{user: user}, &loginTestSessions{hash: string(hash), version: 1})
		if _, _, err := svc.Login(context.Background(), "person@example.com", "Password123"); err == nil {
			t.Fatalf("%s account could log in", name)
		}
	}
}
