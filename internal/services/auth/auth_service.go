package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/idtoken"
)

type authService struct {
	userRepo ports.UserRepository
}

func NewAuthService(repo ports.UserRepository) ports.AuthService {
	return &authService{userRepo: repo}
}

func (s *authService) Login(ctx context.Context, email, password string) (string, *domain.User, error) {
	user, err := s.userRepo.GetUserByEmail(ctx, email)
	if err != nil {
		return "", nil, errors.New("invalid credentials")
	}

	// Validate password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", nil, errors.New("invalid credentials")
	}

	// Generate JWT
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "default_fallback_secret" // For dev
	}

	claims := jwt.MapClaims{
		"sub":  user.ID,
		"role": user.Role,
		"exp":  time.Now().Add(time.Hour * 72).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", nil, err
	}

	return tokenString, user, nil
}

func (s *authService) Register(ctx context.Context, name, username, email, password, role string, dob *string, documentID *string, country *string) (*domain.User, error) {
	username = strings.ToLower(username)
	// 1. Validate if user already exists
	_, err := s.userRepo.GetUserByEmail(ctx, email)
	if err == nil {
		return nil, errors.New("user already exists with this email")
	}

	// 2. Default Role
	if role == "" {
		role = "USER"
	}

	// 3. Hash Password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// 4. Create User Model
	now := time.Now()
	user := &domain.User{
		ID:            uuid.New().String(),
		Email:         email,
		Name:          name,
		Username:      username,
		PasswordHash:  string(hashedPassword),
		Role:          role,
		Dob:           dob,
		DocumentID:    documentID,
		Country:       country,
		Followers:     []string{},
		Following:     []string{},
		IsKycVerified: false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// 5. Save to DB
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (s *authService) GoogleLogin(ctx context.Context, idToken string) (string, *domain.User, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")

	// 1. Verify the token with Google
	payload, err := idtoken.Validate(ctx, idToken, clientID)
	if err != nil {
		return "", nil, errors.New("invalid google token")
	}

	// 2. Extract Data
	email := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	picture, _ := payload.Claims["picture"].(string)

	var user *domain.User

	// 3. Check if user exists
	user, err = s.userRepo.GetUserByEmail(ctx, email)

	if err != nil { // User does not exist, let's register them
		now := time.Now()

		// Generate unique username
		baseUsername := strings.ToLower(strings.Split(email, "@")[0])
		username := baseUsername
		suffix := 1
		for {
			_, err := s.userRepo.GetUserByUsername(ctx, username)
			if err != nil {
				// Error means user not found, which is good (unique)
				break
			}
			username = fmt.Sprintf("%s%d", baseUsername, suffix)
			suffix++
		}

		user = &domain.User{
			ID:            uuid.New().String(),
			Email:         email,
			Name:          name,
			Username:      username,
			PasswordHash:  "[GOOGLE_AUTH]",
			Role:          "USER",
			AvatarURL:     &picture,
			Followers:     []string{},
			Following:     []string{},
			IsKycVerified: false,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if err := s.userRepo.CreateUser(ctx, user); err != nil {
			return "", nil, err
		}
	}

	// 4. Generate our JWT
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "default_fallback_secret" // For dev
	}

	claims := jwt.MapClaims{
		"sub":  user.ID,
		"role": user.Role,
		"exp":  time.Now().Add(time.Hour * 72).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", nil, err
	}

	return tokenString, user, nil
}
