package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type PasswordRecovery struct {
	repo    ports.PasswordRecoveryRepository
	sender  ports.PasswordResetSender
	key     []byte
	baseURL *url.URL
}

func NewPasswordRecovery(repo ports.PasswordRecoveryRepository, sender ports.PasswordResetSender, key, frontendURL string) (*PasswordRecovery, error) {
	s := &PasswordRecovery{repo: repo, sender: sender, key: []byte(key)}
	if sender == nil {
		return s, nil
	}
	if len(key) < 32 {
		return nil, errors.New("PASSWORD_RESET_TOKEN_KEY must contain at least 32 bytes")
	}
	u, err := url.Parse(frontendURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("FRONTEND_URL must be a trusted absolute URL without credentials, query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return nil, errors.New("FRONTEND_URL must use HTTPS (HTTP allowed only on local development loopback)")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/reset-password"
	s.baseURL = u
	return s, nil
}

func (s *PasswordRecovery) Enabled() bool {
	return s.sender != nil && s.baseURL != nil && len(s.key) >= 32
}

func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < 8 || len(password) > 72 || strings.TrimSpace(password) == "" {
		return domain.ErrInvalidPassword
	}
	return nil
}

func hashResetToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *PasswordRecovery) deliveryToken(id string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("root-password-reset-v1:" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *PasswordRecovery) allow(ctx context.Context, scope, value string, limit int, window time.Duration) error {
	allowed, err := s.repo.AllowAttempt(ctx, hashResetToken(scope+":"+value), limit, window)
	if err != nil {
		return fmt.Errorf("authentication rate limit: %w", err)
	}
	if !allowed {
		return domain.ErrAuthRateLimited
	}
	return nil
}

func (s *PasswordRecovery) RequestReset(ctx context.Context, email, clientIP string) error {
	if !s.Enabled() {
		return domain.ErrRecoveryUnavailable
	}
	email = strings.TrimSpace(email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return domain.ErrInvalidRecoveryEmail
	}
	if err := s.allow(ctx, "reset-request-ip", clientIP, 10, 15*time.Minute); err != nil {
		return err
	}
	if err := s.allow(ctx, "reset-request-email", strings.ToLower(email), 3, time.Hour); err != nil {
		return err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("generate password reset nonce")
	}
	id := hex.EncodeToString(nonce)
	// Both registered and absent/OAuth accounts follow the same SQL/response path.
	if err := s.repo.QueuePasswordReset(ctx, id, email, hashResetToken(s.deliveryToken(id)), 30*time.Minute); err != nil {
		return fmt.Errorf("queue password reset: %w", err)
	}
	return nil
}

func (s *PasswordRecovery) ResetPassword(ctx context.Context, token, password, confirmation, clientIP string) error {
	if !s.Enabled() {
		return domain.ErrRecoveryUnavailable
	}
	if err := s.allow(ctx, "reset-validate-ip", clientIP, 10, 15*time.Minute); err != nil {
		return err
	}
	if len(token) != 43 {
		return domain.ErrInvalidResetToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return domain.ErrInvalidResetToken
	}
	hash := hashResetToken(token)
	if err := s.allow(ctx, "reset-validate-token", hash, 5, 15*time.Minute); err != nil {
		return err
	}
	if password != confirmation {
		return domain.ErrInvalidPassword
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("hash reset password")
	}
	return s.repo.ResetPassword(ctx, hash, string(passwordHash))
}

func (s *PasswordRecovery) ProcessNext(ctx context.Context) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	delivery, err := s.repo.ClaimPasswordReset(ctx)
	if err != nil {
		return false, fmt.Errorf("claim reset email: %w", err)
	}
	if delivery == nil {
		return false, nil
	}
	if delivery.UserID == nil {
		return true, s.repo.CompletePasswordResetDelivery(ctx, delivery.ID, false)
	}
	u := *s.baseURL
	u.Fragment = "token=" + s.deliveryToken(delivery.ID)
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = s.sender.SendPasswordReset(sendCtx, delivery.Email, u.String(), delivery.ID)
	cancel()
	if err != nil {
		// Do not log sender errors: provider responses can echo recipients or links.
		if updateErr := s.repo.FailPasswordResetDelivery(ctx, delivery.ID, delivery.Attempts); updateErr != nil {
			return true, errors.New("persist reset email failure")
		}
		return true, errors.New("password reset email was not accepted; delivery will retry when eligible")
	}
	if err := s.repo.CompletePasswordResetDelivery(ctx, delivery.ID, true); err != nil {
		return true, errors.New("confirm reset email delivery")
	}
	return true, nil
}
