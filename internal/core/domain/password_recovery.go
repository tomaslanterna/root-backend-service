package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidResetToken    = errors.New("invalid or expired password reset token")
	ErrInvalidPassword      = errors.New("password must contain at least 8 characters and at most 72 bytes")
	ErrInvalidRecoveryEmail = errors.New("invalid recovery email")
	ErrRecoveryUnavailable  = errors.New("password recovery unavailable")
	ErrAuthRateLimited      = errors.New("too many authentication attempts")
	ErrInvalidSession       = errors.New("invalid session")
)

// Token plaintext is never persisted. ID is an independent random nonce used
// with a server-only HMAC key to reproduce the same token for delivery retries.
type PasswordResetDelivery struct {
	ID             string
	UserID         *string
	Email          string
	Attempts       int
	SessionVersion int64
	ExpiresAt      time.Time
}
