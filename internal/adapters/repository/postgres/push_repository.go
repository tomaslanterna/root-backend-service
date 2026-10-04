package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"root-backend-service/internal/core/domain"
	"time"
)

type PushRepository struct{ db *sql.DB }

func NewPushRepository(db *sql.DB) *PushRepository { return &PushRepository{db: db} }

func (r *PushRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('root_push_schema',0))`); err != nil {
		return err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS push_devices (
 id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token TEXT NOT NULL UNIQUE CHECK(length(token) BETWEEN 20 AND 4096),
 platform TEXT NOT NULL DEFAULT 'android' CHECK(platform='android'), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE INDEX IF NOT EXISTS push_devices_user_idx ON push_devices(user_id)`,
		`CREATE TABLE IF NOT EXISTS push_jobs (
 id BIGSERIAL PRIMARY KEY, device_id UUID NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, token TEXT NOT NULL,
 message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 attempts INTEGER NOT NULL DEFAULT 0, done BOOLEAN NOT NULL DEFAULT FALSE,
 available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(message_id,device_id))`,
		`CREATE INDEX IF NOT EXISTS push_jobs_pending_idx ON push_jobs(available_at,id) WHERE NOT done AND attempts<5`,
		// Same transaction as the message; idempotent message retries cannot enqueue twice.
		`CREATE OR REPLACE FUNCTION root_push_message_enqueue() RETURNS trigger AS $$
 BEGIN
  IF NEW.type IN ('text','image') THEN
   INSERT INTO push_jobs(device_id,user_id,token,message_id)
   SELECT d.id,d.user_id,d.token,NEW.id FROM push_devices d
   JOIN chat_participants cp ON cp.user_id=d.user_id AND cp.chat_id=NEW.chat_id
   WHERE d.user_id<>NEW.sender_id ON CONFLICT DO NOTHING;
  END IF;
  RETURN NEW;
 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS root_push_message_enqueue ON messages`,
		`CREATE TRIGGER root_push_message_enqueue AFTER INSERT ON messages FOR EACH ROW EXECUTE FUNCTION root_push_message_enqueue()`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("push migration: %w", err)
		}
	}
	return tx.Commit()
}

func (r *PushRepository) RegisterDevice(ctx context.Context, userID, id, token string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize reassignment/rotation of one installation or token across API instances.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('root_push_registration',0))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_devices WHERE token=$1 AND id<>$2`, token, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO push_devices(id,user_id,token) VALUES($1,$2,$3)
 ON CONFLICT(id) DO UPDATE SET user_id=EXCLUDED.user_id,token=EXCLUDED.token,updated_at=NOW()`, id, userID, token); err != nil {
		return err
	}
	// Never send queued notifications from an old account or an old token.
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_jobs WHERE device_id=$1 AND (user_id<>$2 OR token<>$3)`, id, userID, token); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PushRepository) RemoveDevice(ctx context.Context, userID, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_devices WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

func (r *PushRepository) ClaimJobs(ctx context.Context) ([]domain.PushJob, error) {
	rows, err := r.db.QueryContext(ctx, `WITH next AS (
 SELECT id FROM push_jobs WHERE NOT done AND attempts<5 AND available_at<=NOW()
 AND created_at>NOW()-INTERVAL '24 hours' ORDER BY available_at,id LIMIT 10 FOR UPDATE SKIP LOCKED
 ) UPDATE push_jobs j SET attempts=attempts+1,available_at=NOW()+INTERVAL '2 minutes'
 FROM next,messages m LEFT JOIN users sender ON sender.id=m.sender_id
 WHERE j.id=next.id AND m.id=j.message_id
 RETURNING j.id,j.attempts,j.device_id,j.user_id,j.token,j.message_id,m.chat_id,
 LEFT(COALESCE(NULLIF(TRIM(sender.name),''),NULLIF(TRIM(sender.username),''),'Nuevo mensaje'),81),
 CASE WHEN m.type='text' THEN LEFT(COALESCE(m.content,''),241) ELSE '' END,COALESCE(m.type,'')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]domain.PushJob, 0)
	for rows.Next() {
		var job domain.PushJob
		if err := rows.Scan(&job.ID, &job.Attempt, &job.DeviceID, &job.UserID, &job.Token, &job.MessageID, &job.ChatID,
			&job.SenderName, &job.Content, &job.MessageType); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *PushRepository) CanSend(ctx context.Context, job domain.PushJob) (bool, error) {
	var allowed bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM push_devices d JOIN chat_participants cp ON cp.user_id=d.user_id AND cp.chat_id=$4
 WHERE d.id=$1 AND d.user_id=$2 AND d.token=$3
 AND NOT EXISTS(SELECT 1 FROM message_receipts WHERE message_id=$5 AND user_id=$2 AND read_at IS NOT NULL))`,
		job.DeviceID, job.UserID, job.Token, job.ChatID, job.MessageID).Scan(&allowed)
	return allowed, err
}

func (r *PushRepository) FinishJob(ctx context.Context, job domain.PushJob, done bool, retry time.Duration) error {
	_, err := r.db.ExecContext(ctx, `UPDATE push_jobs SET done=$3,available_at=NOW()+($4*INTERVAL '1 second') WHERE id=$1 AND attempts=$2`, job.ID, job.Attempt, done, retry.Seconds())
	return err
}

func (r *PushRepository) ExpireToken(ctx context.Context, job domain.PushJob) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_devices WHERE id=$1 AND user_id=$2 AND token=$3`, job.DeviceID, job.UserID, job.Token)
	return err
}

func (r *PushRepository) Prune(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_jobs WHERE created_at<NOW()-INTERVAL '7 days'`)
	return err
}
