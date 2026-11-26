package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"root-backend-service/internal/core/domain"
)

// AppleIdentityRepository implementa ports.AppleIdentityRepository sobre la tabla apple_identities
// (user_id, apple_sub único, client_id, email, refresh_token, created_at, updated_at).
type AppleIdentityRepository struct{ db *sql.DB }

func NewAppleIdentityRepository(db *sql.DB) *AppleIdentityRepository {
	return &AppleIdentityRepository{db: db}
}

func (r *AppleIdentityRepository) UserIDBySubject(ctx context.Context, subject string) (string, error) {
	var userID string
	err := r.db.QueryRowContext(ctx, `SELECT user_id::text FROM apple_identities WHERE apple_sub = $1`, subject).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading apple identity: %w", err)
	}
	return userID, nil
}

func (r *AppleIdentityRepository) Link(ctx context.Context, userID, subject, clientID, email string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO apple_identities (user_id, apple_sub, client_id, email)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (user_id) DO UPDATE SET apple_sub = EXCLUDED.apple_sub, client_id = EXCLUDED.client_id,
			email = COALESCE(EXCLUDED.email, apple_identities.email), updated_at = NOW()`, userID, subject, clientID, email)
	if err != nil {
		return fmt.Errorf("linking apple identity: %w", err)
	}
	return nil
}

func (r *AppleIdentityRepository) SetRefreshToken(ctx context.Context, userID, clientID, refreshToken string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE apple_identities SET refresh_token = $3, client_id = $2, updated_at = NOW()
		WHERE user_id = $1`, userID, clientID, refreshToken)
	if err != nil {
		return fmt.Errorf("saving apple refresh token: %w", err)
	}
	return nil
}

func (r *AppleIdentityRepository) RefreshToken(ctx context.Context, userID string) (string, string, error) {
	var token sql.NullString
	var clientID string
	err := r.db.QueryRowContext(ctx, `SELECT refresh_token, client_id FROM apple_identities WHERE user_id = $1`, userID).Scan(&token, &clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("reading apple refresh token: %w", err)
	}
	return token.String, clientID, nil
}
