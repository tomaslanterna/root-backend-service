package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// EventRRPPRepository guarda los RRPPs oficiales de cada evento en la tabla de relación event_rrpps.
type EventRRPPRepository struct {
	db *sql.DB
}

func NewEventRRPPRepository(db *sql.DB) *EventRRPPRepository {
	return &EventRRPPRepository{db: db}
}

var _ ports.EventRRPPRepository = (*EventRRPPRepository)(nil)

// eventRRPPSchema solo crea objetos nuevos (IF NOT EXISTS): no altera events ni users.
var eventRRPPSchema = []string{
	`CREATE TABLE IF NOT EXISTS event_rrpps (
		event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		position   SMALLINT NOT NULL DEFAULT 0 CHECK (position >= 0),
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (event_id, user_id)
	)`,
	// La PK cubre las búsquedas por evento; este índice cubre el FK a users (borrado en cascada).
	`CREATE INDEX IF NOT EXISTS event_rrpps_user_idx ON event_rrpps (user_id)`,
}

func (r *EventRRPPRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning event rrpps schema migration: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range eventRRPPSchema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing event rrpps statement %q: %w", strings.Join(strings.Fields(stmt), " "), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing event rrpps schema migration: %w", err)
	}
	return nil
}

// ListForEvent trae solo datos públicos del perfil. Si a alguien le quitan el rol RRPP deja de aparecer
// aunque la fila siga en event_rrpps.
func (r *EventRRPPRepository) ListForEvent(ctx context.Context, eventID string) ([]domain.EventRRPP, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT u.id::text, COALESCE(u.name, ''), COALESCE(u.username, ''), NULLIF(u.avatar_url, ''), er.position
		FROM event_rrpps er
		JOIN users u ON u.id = er.user_id
		WHERE er.event_id = $1 AND UPPER(COALESCE(u.role, '')) = 'RRPP'
		ORDER BY er.position ASC, LOWER(COALESCE(u.name, '')) ASC, u.id ASC`, eventID)
	if err != nil {
		return nil, fmt.Errorf("querying event rrpps: %w", err)
	}
	defer rows.Close()

	rrpps := make([]domain.EventRRPP, 0)
	for rows.Next() {
		var item domain.EventRRPP
		var avatar sql.NullString
		if err := rows.Scan(&item.UserID, &item.Name, &item.Username, &avatar, &item.Position); err != nil {
			return nil, fmt.Errorf("scanning event rrpp: %w", err)
		}
		if avatar.Valid {
			item.AvatarURL = &avatar.String
		}
		rrpps = append(rrpps, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading event rrpps: %w", err)
	}
	return rrpps, nil
}

// Assign valida evento y rol dentro de la misma transacción (FOR KEY SHARE evita que se borren mientras tanto)
// y hace upsert de la posición.
func (r *EventRRPPRepository) Assign(ctx context.Context, eventID, userID string, position int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning rrpp assignment: %w", err)
	}
	defer tx.Rollback()

	var found string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM events WHERE id = $1 FOR KEY SHARE`, eventID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrEventNotFound
		}
		return fmt.Errorf("checking rrpp event: %w", err)
	}
	var role sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1 FOR KEY SHARE`, userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		return fmt.Errorf("checking rrpp user: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(role.String), domain.RoleRRPP) {
		return domain.ErrNotRRPP
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_rrpps (event_id, user_id, position) VALUES ($1, $2, $3)
		ON CONFLICT (event_id, user_id) DO UPDATE SET position = EXCLUDED.position`, eventID, userID, position); err != nil {
		return fmt.Errorf("assigning event rrpp: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing rrpp assignment: %w", err)
	}
	return nil
}

func (r *EventRRPPRepository) Remove(ctx context.Context, eventID, userID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM event_rrpps WHERE event_id = $1 AND user_id = $2`, eventID, userID)
	if err != nil {
		return false, fmt.Errorf("removing event rrpp: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting removed event rrpps: %w", err)
	}
	return affected > 0, nil
}
