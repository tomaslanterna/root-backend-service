package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type danceRepository struct {
	db *sql.DB
}

func NewDanceRepository(db *sql.DB) ports.DanceRepository {
	return &danceRepository{db: db}
}

// crewStepsScope define qué sesiones de baile suman para un integrante de una crew (alias: s=squads,
// sm=squad_members, ds=dance_sessions):
//   - crew permanente: solo las sesiones que EMPEZARON después de que la persona se unió a esa crew. No cuenta el
//     histórico anterior. Una sesión es una fila por (persona, evento) que acumula pasos, así que si la persona
//     ya estaba bailando cuando se unió, esa sesión no suma (no se pueden separar los pasos de antes y de después).
//   - crew temporal: únicamente las sesiones de su evento.
const crewStepsScope = `(CASE WHEN s.type = 'temporary'
		THEN ds.event_id = s.event_id
		ELSE ds.start_time >= (COALESCE(sm.joined_at, s.created_at, TIMESTAMP '-infinity') AT TIME ZONE 'UTC') END)`

// InitSchema está congelado: es parte de la migración 1 (migrations/baseline.go). No editar;
// un cambio de esquema nuevo va en un archivo de migrations/sql/.
func (r *danceRepository) InitSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning dance/crew schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`ALTER TABLE squads ALTER COLUMN expires_at DROP NOT NULL`,
		`ALTER TABLE squads ALTER COLUMN expires_at SET DEFAULT (NOW() + INTERVAL '100 years')`,
		`CREATE TABLE IF NOT EXISTS squad_events (
			squad_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE,
			event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (squad_id, event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS squad_events_squad_idx ON squad_events (squad_id)`,
		`CREATE INDEX IF NOT EXISTS squads_type_created_idx ON squads (type, created_at DESC)`,
		// Visibilidad: las crews que ya existían quedan privadas (se entraba solo con invitación).
		`ALTER TABLE squads ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT false`,
		`CREATE INDEX IF NOT EXISTS squads_public_idx ON squads (is_public) WHERE type = 'permanent'`,
		`UPDATE dance_sessions SET is_validated = true WHERE is_validated = false AND steps_count > 0`,
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("applying dance/crew schema migration: %w", err)
		}
	}

	return tx.Commit()
}

func (r *danceRepository) SaveDanceSession(ctx context.Context, session *domain.DanceSession) error {
	upsertQuery := `
		INSERT INTO dance_sessions (id, user_id, event_id, steps_count, start_time, end_time, is_validated, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, event_id) 
		DO UPDATE SET 
			steps_count = dance_sessions.steps_count + EXCLUDED.steps_count,
			end_time = EXCLUDED.end_time,
			is_validated = dance_sessions.is_validated OR EXCLUDED.is_validated
	`
	_, err := r.db.ExecContext(ctx, upsertQuery,
		session.ID, session.UserID, session.EventID, session.StepsCount,
		session.StartTime, session.EndTime, session.IsValidated, session.CreatedAt)

	return err
}

func (r *danceRepository) CreatePermanentCrew(ctx context.Context, creatorID string, req ports.CreateCrewRequest) (map[string]interface{}, error) {
	if _, err := uuid.Parse(creatorID); err != nil {
		return nil, errors.New("invalid creator user ID")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Nueva Crew"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create crew tx: %w", err)
	}
	defer tx.Rollback()

	crewID := uuid.NewString()
	chatID := uuid.NewString()
	inviteCode := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	now := time.Now().UTC()
	expiresAt := now.AddDate(100, 0, 0)

	var eventIDs []string
	seenEvents := make(map[string]bool)
	if req.EventID != nil && strings.TrimSpace(*req.EventID) != "" {
		eid := strings.TrimSpace(*req.EventID)
		if _, err := uuid.Parse(eid); err == nil {
			eventIDs = append(eventIDs, eid)
			seenEvents[eid] = true
		}
	}
	for _, rawID := range req.EventIDs {
		eid := strings.TrimSpace(rawID)
		if _, err := uuid.Parse(eid); err == nil && !seenEvents[eid] {
			eventIDs = append(eventIDs, eid)
			seenEvents[eid] = true
		}
	}

	var primaryEventID interface{}
	if len(eventIDs) > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1::uuid)`, eventIDs[0]).Scan(&exists); err == nil && exists {
			primaryEventID = eventIDs[0]
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO squads (id, event_id, name, chat_room_id, status, created_at, expires_at, type, invite_code, is_public)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, 'active', $5, $6, 'permanent', $7, $8)
	`, crewID, primaryEventID, name, chatID, now, expiresAt, inviteCode, req.IsPublic)
	if err != nil {
		return nil, fmt.Errorf("inserting permanent crew: %w", err)
	}

	// Insert creator as host
	_, err = tx.ExecContext(ctx, `
		INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at)
		VALUES ($1::uuid, $2::uuid, 'host', false, $3)
		ON CONFLICT (squad_id, user_id) DO NOTHING
	`, crewID, creatorID, now)
	if err != nil {
		return nil, fmt.Errorf("inserting crew creator member: %w", err)
	}

	// Insert invited members (only valid existing users)
	for _, memberID := range req.MemberIDs {
		mid := strings.TrimSpace(memberID)
		if mid == "" || mid == creatorID {
			continue
		}
		if _, err := uuid.Parse(mid); err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at)
			SELECT $1::uuid, u.id, 'member', false, $3
			FROM users u
			WHERE u.id = $2::uuid
			ON CONFLICT (squad_id, user_id) DO NOTHING
		`, crewID, mid, now); err != nil {
			return nil, fmt.Errorf("inserting crew member %s: %w", mid, err)
		}
	}

	// Create group chat and participants for the crew
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chats (id, type, last_message, created_at, updated_at)
		VALUES ($1::uuid, 'CREWS', '', $2, $2)
		ON CONFLICT (id) DO NOTHING
	`, chatID, now); err != nil {
		return nil, fmt.Errorf("creating crew chat: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_participants (chat_id, user_id, joined_at)
		SELECT $1::uuid, user_id, joined_at
		FROM squad_members
		WHERE squad_id = $2::uuid
		ON CONFLICT DO NOTHING
	`, chatID, crewID); err != nil {
		return nil, fmt.Errorf("adding crew chat participants: %w", err)
	}

	// Link associated events if provided
	for _, eid := range eventIDs {
		_, _ = tx.ExecContext(ctx, `
			INSERT INTO squad_events (squad_id, event_id, created_at)
			SELECT $1::uuid, e.id, $3
			FROM events e
			WHERE e.id = $2::uuid
			ON CONFLICT (squad_id, event_id) DO NOTHING
		`, crewID, eid, now)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing create crew tx: %w", err)
	}

	return r.GetCrewByID(ctx, crewID)
}

// JoinPermanentCrew mete al usuario en la crew dueña de ese código de invitación. Solo vale el código:
// conocer el id de una crew no alcanza para entrar (el código se puede regenerar para anular un link).
func (r *danceRepository) JoinPermanentCrew(ctx context.Context, userID, codeOrID string) (map[string]interface{}, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, errors.New("invalid user ID")
	}
	cleanCode := strings.TrimSpace(codeOrID)
	if cleanCode == "" {
		return nil, errors.New("invite code is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin join crew tx: %w", err)
	}
	defer tx.Rollback()

	var squadID string
	var chatRoomID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, COALESCE(chat_room_id::text, '')
		FROM squads
		WHERE type = 'permanent' AND COALESCE(invite_code, '') <> '' AND UPPER(invite_code) = UPPER($1)
		LIMIT 1
	`, cleanCode).Scan(&squadID, &chatRoomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("finding crew to join: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at)
		VALUES ($1::uuid, $2::uuid, 'member', false, $3)
		ON CONFLICT (squad_id, user_id) DO NOTHING
	`, squadID, userID, now); err != nil {
		return nil, fmt.Errorf("adding member to crew: %w", err)
	}

	if chatRoomID.Valid && chatRoomID.String != "" {
		_, _ = tx.ExecContext(ctx, `
			INSERT INTO chat_participants (chat_id, user_id, joined_at)
			VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT DO NOTHING
		`, chatRoomID.String, userID, now)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing join crew tx: %w", err)
	}

	return r.GetCrewByID(ctx, squadID)
}

// JoinPublicCrew suma al usuario a una crew pública (y a su chat) sin código. Si la crew no existe o es
// privada devuelve sql.ErrNoRows: a una privada solo se entra con el link de invitación.
func (r *danceRepository) JoinPublicCrew(ctx context.Context, userID, squadID string) (map[string]interface{}, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, errors.New("invalid user ID")
	}
	if _, err := uuid.Parse(squadID); err != nil {
		return nil, sql.ErrNoRows
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin join public crew tx: %w", err)
	}
	defer tx.Rollback()

	var chatRoomID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(chat_room_id::text, '')
		FROM squads
		WHERE id = $1::uuid AND type = 'permanent' AND is_public = true
	`, squadID).Scan(&chatRoomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("finding public crew: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at)
		VALUES ($1::uuid, $2::uuid, 'member', false, $3)
		ON CONFLICT (squad_id, user_id) DO NOTHING
	`, squadID, userID, now); err != nil {
		return nil, fmt.Errorf("adding member to public crew: %w", err)
	}
	if chatRoomID.Valid && chatRoomID.String != "" {
		_, _ = tx.ExecContext(ctx, `
			INSERT INTO chat_participants (chat_id, user_id, joined_at)
			VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT DO NOTHING
		`, chatRoomID.String, userID, now)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing join public crew tx: %w", err)
	}
	return r.GetCrewByID(ctx, squadID)
}

// SetCrewVisibility cambia la crew entre pública y privada. Solo el host (quien la creó).
func (r *danceRepository) SetCrewVisibility(ctx context.Context, squadID, userID string, isPublic bool) error {
	if _, err := uuid.Parse(squadID); err != nil {
		return sql.ErrNoRows
	}
	if _, err := uuid.Parse(userID); err != nil {
		return ports.ErrNotCrewHost
	}
	var exists, isHost bool
	err := r.db.QueryRowContext(ctx, `
		SELECT true, EXISTS(
			SELECT 1 FROM squad_members m WHERE m.squad_id = s.id AND m.user_id = $2::uuid AND m.role = 'host'
		)
		FROM squads s
		WHERE s.id = $1::uuid AND s.type = 'permanent'
	`, squadID, userID).Scan(&exists, &isHost)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("checking crew host: %w", err)
	}
	if !isHost {
		return ports.ErrNotCrewHost
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE squads SET is_public = $2 WHERE id = $1::uuid`, squadID, isPublic); err != nil {
		return fmt.Errorf("updating crew visibility: %w", err)
	}
	return nil
}

func (r *danceRepository) AddCrewMembers(ctx context.Context, squadID string, memberIDs []string, actorUserID string) (map[string]interface{}, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return nil, errors.New("invalid crew ID")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin add crew members tx: %w", err)
	}
	defer tx.Rollback()

	var chatRoomID sql.NullString
	var crewType string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(chat_room_id::text, ''), COALESCE(type, 'permanent')
		FROM squads
		WHERE id = $1::uuid
		LIMIT 1
	`, squadID).Scan(&chatRoomID, &crewType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("finding crew to add members: %w", err)
	}
	// Las crews temporales solo admiten gente con solicitudes que aprueba el host.
	if crewType == domain.CrewTypeTemporary {
		return nil, domain.ErrTempCrewManaged
	}

	now := time.Now().UTC()
	for _, rawID := range memberIDs {
		mid := strings.TrimSpace(rawID)
		if mid == "" {
			continue
		}
		if _, err := uuid.Parse(mid); err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at)
			SELECT $1::uuid, u.id, 'member', false, $3
			FROM users u
			WHERE u.id = $2::uuid
			ON CONFLICT (squad_id, user_id) DO NOTHING
		`, squadID, mid, now); err != nil {
			return nil, fmt.Errorf("inserting crew member %s: %w", mid, err)
		}

		if chatRoomID.Valid && chatRoomID.String != "" {
			_, _ = tx.ExecContext(ctx, `
				INSERT INTO chat_participants (chat_id, user_id, joined_at)
				SELECT $1::uuid, u.id, $3
				FROM users u
				WHERE u.id = $2::uuid
				ON CONFLICT DO NOTHING
			`, chatRoomID.String, mid, now)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing add crew members tx: %w", err)
	}

	return r.GetCrewByID(ctx, squadID)
}

func (r *danceRepository) AddCrewEvent(ctx context.Context, squadID, eventID, userID string) error {
	if _, err := uuid.Parse(squadID); err != nil {
		return errors.New("invalid crew ID")
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return errors.New("invalid event ID")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squads WHERE id = $1::uuid)`, squadID).Scan(&exists); err != nil || !exists {
		return sql.ErrNoRows
	}
	var temporary bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(type, 'permanent') = 'temporary' FROM squads WHERE id = $1::uuid`, squadID).Scan(&temporary); err != nil {
		return err
	}
	if temporary {
		return domain.ErrTempCrewManaged
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1::uuid)`, eventID).Scan(&exists); err != nil || !exists {
		return sql.ErrNoRows
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO squad_events (squad_id, event_id, created_at)
		VALUES ($1::uuid, $2::uuid, NOW())
		ON CONFLICT (squad_id, event_id) DO NOTHING
	`, squadID, eventID); err != nil {
		return err
	}

	_, _ = tx.ExecContext(ctx, `
		UPDATE squads SET event_id = COALESCE(event_id, $2::uuid) WHERE id = $1::uuid
	`, squadID, eventID)

	return tx.Commit()
}

func (r *danceRepository) GetCrewLeaderboardAllTime(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return []map[string]interface{}{}, nil
	}

	query := `
		SELECT u.id, u.name, u.username, u.avatar_url, COALESCE(sm.role, 'member') as role,
		       COALESCE(SUM(ds.steps_count), 0) as total_steps
		FROM squad_members sm
		JOIN squads s ON s.id = sm.squad_id
		JOIN users u ON sm.user_id = u.id
		LEFT JOIN dance_sessions ds ON u.id = ds.user_id AND ` + crewStepsScope + `
		WHERE sm.squad_id = $1::uuid
		GROUP BY u.id, u.name, u.username, u.avatar_url, sm.role, sm.joined_at
		ORDER BY total_steps DESC, sm.joined_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	leaderboard := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, name, username, role string
		var avatarURL *string
		var totalSteps int
		if err := rows.Scan(&id, &name, &username, &avatarURL, &role, &totalSteps); err != nil {
			return nil, err
		}
		leaderboard = append(leaderboard, map[string]interface{}{
			"userId":    id,
			"name":      name,
			"username":  username,
			"avatarUrl": avatarURL,
			"role":      role,
			"steps":     totalSteps,
		})
	}
	return leaderboard, nil
}

func (r *danceRepository) GetCrewLeaderboardByEvent(ctx context.Context, squadID, eventID string) ([]map[string]interface{}, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return []map[string]interface{}{}, nil
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return []map[string]interface{}{}, nil
	}

	query := `
		SELECT u.id, u.name, u.username, u.avatar_url, COALESCE(sm.role, 'member') as role,
		       COALESCE(SUM(ds.steps_count), 0) as total_steps
		FROM squad_members sm
		JOIN squads s ON s.id = sm.squad_id
		JOIN users u ON sm.user_id = u.id
		LEFT JOIN dance_sessions ds ON u.id = ds.user_id AND ds.event_id = $2::uuid AND ` + crewStepsScope + `
		WHERE sm.squad_id = $1::uuid
		GROUP BY u.id, u.name, u.username, u.avatar_url, sm.role, sm.joined_at
		ORDER BY total_steps DESC, sm.joined_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	leaderboard := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, name, username, role string
		var avatarURL *string
		var totalSteps int
		if err := rows.Scan(&id, &name, &username, &avatarURL, &role, &totalSteps); err != nil {
			return nil, err
		}
		leaderboard = append(leaderboard, map[string]interface{}{
			"userId":    id,
			"name":      name,
			"username":  username,
			"avatarUrl": avatarURL,
			"role":      role,
			"steps":     totalSteps,
		})
	}
	return leaderboard, nil
}

func (r *danceRepository) GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return []map[string]interface{}{}, nil
	}

	query := `
		WITH crew_steps AS (
			SELECT sm.squad_id, sm.user_id, COALESCE(SUM(ds.steps_count), 0) as total_steps
			FROM squad_members sm
			JOIN squads s ON s.id = sm.squad_id
			LEFT JOIN dance_sessions ds ON ds.user_id = sm.user_id AND ` + crewStepsScope + `
			GROUP BY sm.squad_id, sm.user_id
		),
		crew_ranks AS (
			SELECT squad_id, user_id, total_steps,
				RANK() OVER (PARTITION BY squad_id ORDER BY total_steps DESC) as rank
			FROM crew_steps
		),
		crew_totals AS (
			SELECT squad_id,
				COALESCE(MAX(total_steps), 0) as top_steps,
				COALESCE(SUM(total_steps), 0) as crew_total_steps
			FROM crew_steps
			GROUP BY squad_id
		)
		SELECT s.id, s.name, COALESCE(s.type, 'permanent'), s.invite_code, s.status,
		       COALESCE(s.chat_room_id::text, ''), COALESCE(s.is_public, false),
		       (SELECT COUNT(*) FROM squad_members WHERE squad_id = s.id) as member_count,
		       COALESCE(ct.top_steps, 0) as top_steps,
		       COALESCE(ct.crew_total_steps, 0) as total_steps,
		       COALESCE(cr.rank, 0) as user_rank,
		       COALESCE((
		           SELECT jsonb_agg(
		               jsonb_build_object(
		                   'userId', u.id,
		                   'name', u.name,
		                   'username', u.username,
		                   'avatarUrl', u.avatar_url,
		                   'role', COALESCE(m.role, 'member')
		               ) ORDER BY m.joined_at ASC
		           )
		           FROM squad_members m
		           JOIN users u ON u.id = m.user_id
		           WHERE m.squad_id = s.id
		       ), '[]'::jsonb) as members_json
		FROM squads s
		JOIN squad_members sm ON sm.squad_id = s.id AND sm.user_id = $1::uuid
		LEFT JOIN crew_totals ct ON ct.squad_id = s.id
		LEFT JOIN crew_ranks cr ON cr.squad_id = s.id AND cr.user_id = $1::uuid
		WHERE s.type = 'permanent'
		ORDER BY s.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	crews := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, name, crewType, status, chatRoomID string
		var inviteCode *string
		var isPublic bool
		var memberCount, topSteps, totalSteps, userRank int
		var membersBytes []byte
		if err := rows.Scan(&id, &name, &crewType, &inviteCode, &status, &chatRoomID, &isPublic, &memberCount, &topSteps, &totalSteps, &userRank, &membersBytes); err != nil {
			return nil, err
		}
		var members []map[string]interface{}
		_ = json.Unmarshal(membersBytes, &members)

		crews = append(crews, map[string]interface{}{
			"id":          id,
			"name":        name,
			"type":        crewType,
			"inviteCode":  inviteCode,
			"status":      status,
			"chatRoomId":  chatRoomID,
			"isPublic":    isPublic,
			"memberCount": memberCount,
			"topSteps":    topSteps,
			"totalSteps":  totalSteps,
			"userRank":    userRank,
			"members":     members,
		})
	}
	return crews, nil
}

func (r *danceRepository) GetAllCrews(ctx context.Context) ([]map[string]interface{}, error) {
	query := `
		WITH crew_steps AS (
			SELECT sm.squad_id, sm.user_id, COALESCE(SUM(ds.steps_count), 0) as total_steps
			FROM squad_members sm
			JOIN squads s ON s.id = sm.squad_id
			LEFT JOIN dance_sessions ds ON ds.user_id = sm.user_id AND ` + crewStepsScope + `
			GROUP BY sm.squad_id, sm.user_id
		),
		crew_totals AS (
			SELECT squad_id,
				COALESCE(MAX(total_steps), 0) as top_steps,
				COALESCE(SUM(total_steps), 0) as crew_total_steps
			FROM crew_steps
			GROUP BY squad_id
		)
		SELECT s.id, s.name, COALESCE(s.type, 'permanent'), s.invite_code, s.status,
		       COALESCE(s.chat_room_id::text, ''), COALESCE(s.is_public, false),
		       (SELECT COUNT(*) FROM squad_members WHERE squad_id = s.id) as member_count,
		       COALESCE(ct.top_steps, 0) as top_steps,
		       COALESCE(ct.crew_total_steps, 0) as total_steps,
		       0 as user_rank,
		       COALESCE((
		           SELECT jsonb_agg(
		               jsonb_build_object(
		                   'userId', u.id,
		                   'name', u.name,
		                   'username', u.username,
		                   'avatarUrl', u.avatar_url,
		                   'role', COALESCE(m.role, 'member')
		               ) ORDER BY m.joined_at ASC
		           )
		           FROM squad_members m
		           JOIN users u ON u.id = m.user_id
		           WHERE m.squad_id = s.id
		       ), '[]'::jsonb) as members_json
		FROM squads s
		LEFT JOIN crew_totals ct ON ct.squad_id = s.id
		WHERE s.type = 'permanent' AND s.is_public = true
		ORDER BY COALESCE(ct.crew_total_steps, 0) DESC, s.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	crews := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, name, crewType, status, chatRoomID string
		var inviteCode *string
		var isPublic bool
		var memberCount, topSteps, totalSteps, userRank int
		var membersBytes []byte
		if err := rows.Scan(&id, &name, &crewType, &inviteCode, &status, &chatRoomID, &isPublic, &memberCount, &topSteps, &totalSteps, &userRank, &membersBytes); err != nil {
			return nil, err
		}
		var members []map[string]interface{}
		_ = json.Unmarshal(membersBytes, &members)

		crews = append(crews, map[string]interface{}{
			"id":          id,
			"name":        name,
			"type":        crewType,
			"inviteCode":  inviteCode,
			"status":      status,
			"chatRoomId":  chatRoomID,
			"isPublic":    isPublic,
			"memberCount": memberCount,
			"topSteps":    topSteps,
			"totalSteps":  totalSteps,
			"userRank":    userRank,
			"members":     members,
		})
	}
	return crews, nil
}

func (r *danceRepository) GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	query := `
		SELECT ds.id, ds.event_id, e.title, ds.steps_count, 
		       ds.start_time, ds.end_time, ds.is_validated
		FROM dance_sessions ds
		JOIN events e ON e.id = ds.event_id
		WHERE ds.user_id = $1
		ORDER BY ds.start_time DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []map[string]interface{}
	for rows.Next() {
		var id, eventID, title string
		var stepsCount int
		var startTime, endTime string
		var isValidated bool
		if err := rows.Scan(&id, &eventID, &title, &stepsCount, &startTime, &endTime, &isValidated); err != nil {
			return nil, err
		}
		sessions = append(sessions, map[string]interface{}{
			"id":          id,
			"eventId":     eventID,
			"eventTitle":  title,
			"stepsCount":  stepsCount,
			"startTime":   startTime,
			"endTime":     endTime,
			"isValidated": isValidated,
		})
	}
	return sessions, nil
}

func (r *danceRepository) GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return nil, sql.ErrNoRows
	}

	query := `
		SELECT s.id, s.name, COALESCE(s.type, 'permanent'), s.invite_code, s.status,
		       COALESCE(s.chat_room_id::text, ''), COALESCE(s.is_public, false),
		       COUNT(sm.user_id) as member_count
		FROM squads s
		LEFT JOIN squad_members sm ON sm.squad_id = s.id
		WHERE s.id = $1::uuid
		GROUP BY s.id, s.name, s.type, s.invite_code, s.status, s.chat_room_id, s.is_public
	`
	var id, name, crewType, status, chatRoomID string
	var inviteCode *string
	var isPublic bool
	var memberCount int
	err := r.db.QueryRowContext(ctx, query, squadID).Scan(&id, &name, &crewType, &inviteCode, &status, &chatRoomID, &isPublic, &memberCount)
	if err != nil {
		return nil, err
	}

	leaderboardAllTime, err := r.GetCrewLeaderboardAllTime(ctx, squadID)
	if err != nil {
		leaderboardAllTime = []map[string]interface{}{}
	}

	topSteps := 0
	totalSteps := 0
	for i, entry := range leaderboardAllTime {
		if steps, ok := entry["steps"].(int); ok {
			if i == 0 {
				topSteps = steps
			}
			totalSteps += steps
		}
	}

	events, err := r.GetCrewEvents(ctx, squadID)
	if err != nil {
		events = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"id":                 id,
		"name":               name,
		"type":               crewType,
		"inviteCode":         inviteCode,
		"invite_code":        inviteCode,
		"status":             status,
		"chatRoomId":         chatRoomID,
		"isPublic":           isPublic,
		"memberCount":        memberCount,
		"topSteps":           topSteps,
		"totalSteps":         totalSteps,
		"members":            leaderboardAllTime,
		"leaderboardAllTime": leaderboardAllTime,
		"events":             events,
	}, nil
}

func (r *danceRepository) IsCrewMember(ctx context.Context, squadID, userID string) (bool, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return false, nil
	}
	if _, err := uuid.Parse(userID); err != nil {
		return false, nil
	}
	var member bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squad_members WHERE squad_id = $1::uuid AND user_id = $2::uuid)`, squadID, userID).Scan(&member)
	return member, err
}

// RegenerateCrewInviteCode crea un código nuevo para la crew; el anterior deja de funcionar al instante.
// Puede hacerlo cualquier miembro. Devuelve sql.ErrNoRows si la crew no existe o el usuario no es miembro.
func (r *danceRepository) RegenerateCrewInviteCode(ctx context.Context, squadID, userID string) (string, error) {
	member, err := r.IsCrewMember(ctx, squadID, userID)
	if err != nil {
		return "", fmt.Errorf("checking crew membership: %w", err)
	}
	if !member {
		return "", sql.ErrNoRows
	}
	for attempt := 0; attempt < 5; attempt++ {
		code := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
		result, err := r.db.ExecContext(ctx, `
			UPDATE squads s SET invite_code = $1
			WHERE s.id = $2::uuid AND COALESCE(s.type, 'permanent') = 'permanent'
			  AND NOT EXISTS (SELECT 1 FROM squads o WHERE UPPER(o.invite_code) = $1)
		`, code, squadID)
		if err != nil {
			return "", fmt.Errorf("regenerating invite code: %w", err)
		}
		if n, _ := result.RowsAffected(); n == 1 {
			return code, nil
		}
		// 0 filas: la crew no es permanente o el código ya existía; si la crew existe se reintenta con otro código.
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squads WHERE id = $1::uuid AND COALESCE(type, 'permanent') = 'permanent')`, squadID).Scan(&exists); err != nil || !exists {
			return "", sql.ErrNoRows
		}
	}
	return "", errors.New("could not generate a unique invite code")
}

// GetCrewInvitePreview devuelve lo mínimo para mostrar "te invitaron a esta crew" antes de unirse:
// nombre, cantidad de miembros y algunos miembros. No expone pasos, eventos ni el chat.
func (r *danceRepository) GetCrewInvitePreview(ctx context.Context, inviteCode, viewerID string) (map[string]interface{}, error) {
	code := strings.TrimSpace(inviteCode)
	if code == "" {
		return nil, sql.ErrNoRows
	}
	var id, name string
	var memberCount int
	err := r.db.QueryRowContext(ctx, `
		SELECT s.id::text, s.name, (SELECT COUNT(*) FROM squad_members sm WHERE sm.squad_id = s.id)
		FROM squads s
		WHERE COALESCE(s.type, 'permanent') = 'permanent' AND COALESCE(s.invite_code, '') <> '' AND UPPER(s.invite_code) = UPPER($1)
		LIMIT 1
	`, code).Scan(&id, &name, &memberCount)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT u.name, u.username, u.avatar_url
		FROM squad_members sm JOIN users u ON u.id = sm.user_id
		WHERE sm.squad_id = $1::uuid
		ORDER BY sm.joined_at ASC
		LIMIT 5
	`, id)
	if err != nil {
		return nil, fmt.Errorf("loading crew members preview: %w", err)
	}
	defer rows.Close()
	members := []map[string]interface{}{}
	for rows.Next() {
		var mName, mUsername string
		var avatar *string
		if err := rows.Scan(&mName, &mUsername, &avatar); err != nil {
			return nil, err
		}
		members = append(members, map[string]interface{}{"name": mName, "username": mUsername, "avatarUrl": avatar})
	}

	already, _ := r.IsCrewMember(ctx, id, viewerID)
	return map[string]interface{}{
		"crewId":        id,
		"name":          name,
		"memberCount":   memberCount,
		"members":       members,
		"alreadyMember": already,
	}, nil
}

func (r *danceRepository) GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	if _, err := uuid.Parse(squadID); err != nil {
		return []map[string]interface{}{}, nil
	}

	query := `
		WITH crew_event_ids AS (
			SELECT event_id FROM squad_events WHERE squad_id = $1::uuid
			UNION
			SELECT event_id FROM squads WHERE id = $1::uuid AND event_id IS NOT NULL
			UNION
			SELECT ds.event_id
			FROM dance_sessions ds
			JOIN squad_members sm ON ds.user_id = sm.user_id
			WHERE sm.squad_id = $1::uuid
			UNION
			SELECT er.event_id
			FROM event_rsvps er
			JOIN squad_members sm ON er.user_id = sm.user_id
			WHERE sm.squad_id = $1::uuid AND er.status = 'going'
		)
		SELECT e.id, e.title, e.date, e.location, e.price, e.cinematic_banner_url,
		       COALESCE((
		           SELECT SUM(ds.steps_count)
		           FROM dance_sessions ds
		           JOIN squad_members sm ON sm.user_id = ds.user_id
		           WHERE sm.squad_id = $1::uuid AND ds.event_id = e.id
		       ), 0) AS crew_steps,
		       COALESCE((
		           SELECT COUNT(*)
		           FROM event_rsvps er
		           WHERE er.event_id = e.id AND er.status = 'going'
		       ), 0) AS going_count
		FROM events e
		JOIN crew_event_ids cei ON cei.event_id = e.id
		ORDER BY e.date DESC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, title, location string
		var date time.Time
		var price *float64
		var cinematicBannerURL *string
		var crewSteps, goingCount int
		if err := rows.Scan(&id, &title, &date, &location, &price, &cinematicBannerURL, &crewSteps, &goingCount); err != nil {
			return nil, err
		}

		var finalPrice float64
		if price != nil {
			finalPrice = *price
		}

		events = append(events, map[string]interface{}{
			"id":                 id,
			"title":              title,
			"date":               date.Format(time.RFC3339),
			"location":           location,
			"price":              finalPrice,
			"cinematicBannerUrl": cinematicBannerURL,
			"crewSteps":          crewSteps,
			"goingCount":         goingCount,
		})
	}
	return events, nil
}

func (r *danceRepository) GetEventLiveRanking(ctx context.Context, eventID string) (map[string]interface{}, error) {
	if _, err := uuid.Parse(eventID); err != nil {
		return map[string]interface{}{
			"eventId":         eventID,
			"totalDancers":    0,
			"activeDancers":   0,
			"totalEventSteps": 0,
			"data":            []map[string]interface{}{},
		}, nil
	}

	query := `
		WITH event_participants AS (
			SELECT user_id,
			       MIN(confirmed_at) AS joined_at,
			       BOOL_OR(departed_at IS NULL) AS in_venue
			FROM event_attendance
			WHERE event_id = $1::uuid
			GROUP BY user_id
			UNION
			SELECT user_id,
			       MIN(created_at) AS joined_at,
			       BOOL_OR(end_time >= NOW() - INTERVAL '45 minutes') AS in_venue
			FROM dance_sessions
			WHERE event_id = $1::uuid
			GROUP BY user_id
		),
		merged_participants AS (
			SELECT user_id,
			       MIN(joined_at) AS joined_at,
			       BOOL_OR(in_venue) AS is_active
			FROM event_participants
			GROUP BY user_id
		),
		user_event_steps AS (
			SELECT user_id,
			       COALESCE(SUM(steps_count), 0) AS steps,
			       MAX(end_time) AS last_sync
			FROM dance_sessions
			WHERE event_id = $1::uuid
			GROUP BY user_id
		)
		SELECT u.id,
		       u.name,
		       u.username,
		       u.avatar_url,
		       COALESCE(ues.steps, 0) AS steps,
		       COALESCE(mp.is_active, false) OR (ues.last_sync IS NOT NULL AND ues.last_sync >= NOW() - INTERVAL '45 minutes') AS is_active,
		       COALESCE(ues.last_sync, mp.joined_at, NOW()) AS updated_at
		FROM merged_participants mp
		JOIN users u ON u.id = mp.user_id
		LEFT JOIN user_event_steps ues ON ues.user_id = mp.user_id
		ORDER BY steps DESC, mp.joined_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]map[string]interface{}, 0)
	totalEventSteps := 0
	activeDancers := 0
	rank := 1

	for rows.Next() {
		var userID, name, username string
		var avatarURL *string
		var steps int
		var isActive bool
		var updatedAt time.Time
		if err := rows.Scan(&userID, &name, &username, &avatarURL, &steps, &isActive, &updatedAt); err != nil {
			return nil, err
		}
		totalEventSteps += steps
		if isActive {
			activeDancers++
		}
		entries = append(entries, map[string]interface{}{
			"rank":      rank,
			"userId":    userID,
			"name":      name,
			"username":  username,
			"avatarUrl": avatarURL,
			"steps":     steps,
			"isActive":  isActive,
			"updatedAt": updatedAt.Format(time.RFC3339),
		})
		rank++
	}

	return map[string]interface{}{
		"eventId":         eventID,
		"totalDancers":    len(entries),
		"activeDancers":   activeDancers,
		"totalEventSteps": totalEventSteps,
		"data":            entries,
	}, nil
}
