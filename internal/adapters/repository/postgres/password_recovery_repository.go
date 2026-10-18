package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"time"
)

type PasswordRecoveryRepository struct{ db *sql.DB }

func NewPasswordRecoveryRepository(db *sql.DB) *PasswordRecoveryRepository {
	return &PasswordRecoveryRepository{db: db}
}

func (r *PasswordRecoveryRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_password_recovery_schema',0))`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS users_recovery_email_idx ON users(lower(email))`,
		`CREATE TABLE IF NOT EXISTS password_reset_deliveries (
   id CHAR(64) PRIMARY KEY, user_id UUID REFERENCES users(id) ON DELETE CASCADE,
   email TEXT NOT NULL, token_hash CHAR(64) NOT NULL UNIQUE,
   session_version BIGINT NOT NULL DEFAULT 0,
   status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','sent','ignored','failed','consumed')),
   attempts INTEGER NOT NULL DEFAULT 0, available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
   expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), consumed_at TIMESTAMPTZ)`,
		`CREATE INDEX IF NOT EXISTS password_reset_pending_idx ON password_reset_deliveries(available_at,id) WHERE status IN ('pending','processing')`,
		`CREATE INDEX IF NOT EXISTS password_reset_user_idx ON password_reset_deliveries(user_id)`,
		`CREATE INDEX IF NOT EXISTS password_reset_expiry_idx ON password_reset_deliveries(expires_at)`,
		`CREATE TABLE IF NOT EXISTS auth_rate_limits (key CHAR(64) PRIMARY KEY, attempts INTEGER NOT NULL, expires_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS auth_rate_limits_expiry_idx ON auth_rate_limits(expires_at)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("password recovery migration: %w", err)
		}
	}
	return tx.Commit()
}

func (r *PasswordRecoveryRepository) AllowAttempt(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	var attempts int
	err := r.db.QueryRowContext(ctx, `INSERT INTO auth_rate_limits(key,attempts,expires_at)
 VALUES($1,1,NOW()+$3*INTERVAL '1 second') ON CONFLICT(key) DO UPDATE
 SET attempts=CASE WHEN auth_rate_limits.expires_at<=NOW() THEN 1 ELSE LEAST(auth_rate_limits.attempts+1,$2+1) END,
 expires_at=CASE WHEN auth_rate_limits.expires_at<=NOW() THEN EXCLUDED.expires_at ELSE auth_rate_limits.expires_at END
 RETURNING attempts`, key, limit, window.Seconds()).Scan(&attempts)
	return attempts <= limit, err
}

func (r *PasswordRecoveryRepository) QueuePasswordReset(ctx context.Context, id, email, tokenHash string, ttl time.Duration) error {
	// Ambiguous case-insensitive emails and OAuth-only users are never eligible.
	_, err := r.db.ExecContext(ctx, `INSERT INTO password_reset_deliveries(id,user_id,email,token_hash,session_version,expires_at)
 SELECT $1,u.id,$2,$3,COALESCE(u.session_version,0),NOW()+$4*INTERVAL '1 second'
 FROM (SELECT 1) dummy LEFT JOIN users u ON lower(u.email)=lower($2)
 AND u.password_hash ~ '^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$'
 AND (SELECT COUNT(*) FROM users WHERE lower(email)=lower($2))=1`, id, email, tokenHash, ttl.Seconds())
	return err
}

func (r *PasswordRecoveryRepository) ClaimPasswordReset(ctx context.Context) (*domain.PasswordResetDelivery, error) {
	// Small bounded cleanups retain failures briefly for operational diagnosis, without tokens or durable email history.
	for _, query := range []string{
		`DELETE FROM auth_rate_limits WHERE key IN (SELECT key FROM auth_rate_limits WHERE expires_at<NOW()-INTERVAL '1 day' LIMIT 100)`,
		`DELETE FROM password_reset_deliveries WHERE id IN (SELECT id FROM password_reset_deliveries WHERE expires_at<NOW()-INTERVAL '1 day' LIMIT 100)`,
	} {
		if _, err := r.db.ExecContext(ctx, query); err != nil {
			return nil, err
		}
	}
	var delivery domain.PasswordResetDelivery
	var userID sql.NullString
	err := r.db.QueryRowContext(ctx, `WITH next AS (
 SELECT id FROM password_reset_deliveries WHERE status IN ('pending','processing') AND attempts<5 AND available_at<=NOW() AND expires_at>NOW()
 ORDER BY available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 UPDATE password_reset_deliveries d SET status='processing',attempts=attempts+1,available_at=NOW()+INTERVAL '2 minutes'
 FROM next WHERE d.id=next.id
 RETURNING d.id,CASE WHEN EXISTS(SELECT 1 FROM users u WHERE u.id=d.user_id AND u.session_version=d.session_version) THEN d.user_id ELSE NULL END,
 d.email,d.attempts,d.session_version,d.expires_at`).Scan(&delivery.ID, &userID, &delivery.Email, &delivery.Attempts, &delivery.SessionVersion, &delivery.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		delivery.UserID = &userID.String
	}
	return &delivery, nil
}

func (r *PasswordRecoveryRepository) CompletePasswordResetDelivery(ctx context.Context, id string, sent bool) error {
	status := "ignored"
	if sent {
		status = "sent"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE password_reset_deliveries SET status=$2 WHERE id=$1 AND status='processing'`, id, status)
	return err
}

func (r *PasswordRecoveryRepository) FailPasswordResetDelivery(ctx context.Context, id string, attempts int) error {
	retry := time.Minute * time.Duration(1<<min(attempts-1, 4))
	_, err := r.db.ExecContext(ctx, `UPDATE password_reset_deliveries SET status=CASE WHEN attempts>=5 THEN 'failed' ELSE 'pending' END,
 available_at=NOW()+$2*INTERVAL '1 second' WHERE id=$1 AND status='processing'`, id, retry.Seconds())
	return err
}

func (r *PasswordRecoveryRepository) ResetPassword(ctx context.Context, tokenHash, passwordHash string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM password_reset_deliveries WHERE token_hash=$1 AND user_id IS NOT NULL AND status='sent' AND expires_at>clock_timestamp()`, tokenHash).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrInvalidResetToken
	}
	if err != nil {
		return err
	}
	// Serialize ALL tokens for one user, not just requests carrying the same token.
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT session_version FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&version); err != nil {
		return err
	}
	var tokenVersion int64
	err = tx.QueryRowContext(ctx, `SELECT session_version FROM password_reset_deliveries WHERE token_hash=$1 AND status='sent' AND expires_at>clock_timestamp() FOR UPDATE`, tokenHash).Scan(&tokenVersion)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && version != tokenVersion) {
		return domain.ErrInvalidResetToken
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=$2,session_version=session_version+1,updated_at=NOW() WHERE id=$1`, userID, passwordHash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE password_reset_deliveries SET status='consumed',consumed_at=NOW() WHERE user_id=$1 AND status<>'consumed'`, userID); err != nil {
		return err
	}
	// Push registrations are session-bound. Pending jobs cascade with the devices.
	if _, err = tx.ExecContext(ctx, `DELETE FROM push_devices WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PasswordRecoveryRepository) GetPasswordAndSessionVersion(ctx context.Context, userID string) (string, int64, error) {
	var hash string
	var version int64
	err := r.db.QueryRowContext(ctx, `SELECT password_hash,session_version FROM users WHERE id=$1`, userID).Scan(&hash, &version)
	return hash, version, err
}

func (r *PasswordRecoveryRepository) GetSessionVersion(ctx context.Context, userID string) (int64, error) {
	var version int64
	err := r.db.QueryRowContext(ctx, `SELECT session_version FROM users WHERE id=$1`, userID).Scan(&version)
	return version, err
}
