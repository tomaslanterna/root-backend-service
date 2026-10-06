package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"time"
)

// One query hydrates squads and their members without per-squad queries.
const matcherSquadsQuery = `SELECT s.id, COALESCE(s.event_id::text,''), s.name,
 COALESCE(e.title,''), COALESCE(e.cinematic_banner_url,''), COALESCE(e.location,''),
 COALESCE(s.chat_room_id::text,''), s.status, s.created_at, s.expires_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('userId',sm.user_id,'name',u.name,
 'username',u.username,'avatarUrl',u.avatar_url,'hasTicket',COALESCE(sm.has_ticket,false),
 'joinedAt',to_char(sm.joined_at,'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'role',COALESCE(sm.role,'member'))
 ORDER BY sm.joined_at,sm.user_id) FROM squad_members sm JOIN users u ON u.id=sm.user_id
 WHERE sm.squad_id=s.id),'[]'::jsonb)
 FROM squads s LEFT JOIN events e ON e.id=s.event_id `

type squadQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func queryMatcherSquads(ctx context.Context, q squadQueryer, where string, args ...interface{}) ([]domain.MatcherSquad, error) {
	rows, err := q.QueryContext(ctx, matcherSquadsQuery+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query matcher squads: %w", err)
	}
	defer rows.Close()
	squads := make([]domain.MatcherSquad, 0)
	for rows.Next() {
		var squad domain.MatcherSquad
		var members []byte
		if err := rows.Scan(&squad.ID, &squad.EventID, &squad.Name, &squad.EventTitle, &squad.EventImage, &squad.Location, &squad.ChatRoomID, &squad.Status, &squad.CreatedAt, &squad.ExpiresAt, &members); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(members, &squad.Members); err != nil {
			return nil, err
		}
		squads = append(squads, squad)
	}
	return squads, rows.Err()
}

func (r *MatchRepository) GetUserSquads(ctx context.Context, userID string) ([]domain.MatcherSquad, error) {
	return queryMatcherSquads(ctx, r.db, `WHERE s.type='event_match' AND EXISTS(SELECT 1 FROM squad_members mine WHERE mine.squad_id=s.id AND mine.user_id=$1) ORDER BY s.created_at DESC,s.id`, userID)
}

// The existing pair-matching policy is unchanged. Serializing on the event makes
// repeat/concurrent swipes safe; squad, chat, membership and claims commit together.
func (r *MatchRepository) ProcessSwipe(ctx context.Context, swipe *domain.EventSwipe) (*domain.MatcherSquad, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var eventID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM events WHERE id=$1 FOR UPDATE`, swipe.EventID).Scan(&eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrSquadNotFound
		}
		return nil, err
	}
	existing, err := queryMatcherSquads(ctx, tx, `WHERE s.event_id=$1 AND s.type='event_match' AND s.status<>'archived' AND EXISTS(SELECT 1 FROM squad_members WHERE squad_id=s.id AND user_id=$2) ORDER BY s.created_at DESC LIMIT 1`, swipe.EventID, swipe.UserID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		chatID, err := ensureSquadChat(ctx, tx, existing[0].ID, swipe.UserID)
		if err != nil {
			return nil, err
		}
		existing[0].ChatRoomID = chatID
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &existing[0], nil
	}
	preferences, err := json.Marshal(swipe.Preferences)
	if err != nil {
		return nil, err
	}
	// One pending choice per user/event, without changing historical matched swipes.
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_swipes WHERE event_id=$1 AND user_id=$2 AND has_crew=false`, swipe.EventID, swipe.UserID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO event_swipes(id,user_id,event_id,direction,preferences,has_crew,created_at) VALUES($1,$2,$3,$4,$5,false,$6)`, swipe.ID, swipe.UserID, swipe.EventID, swipe.Direction, preferences, swipe.CreatedAt); err != nil {
		return nil, err
	}
	if swipe.Direction == "pass" {
		return nil, tx.Commit()
	}
	var otherID, otherUser string
	err = tx.QueryRowContext(ctx, `SELECT es.id,es.user_id FROM event_swipes es WHERE es.event_id=$1 AND es.user_id<>$2 AND es.has_crew=false AND es.direction IN('like','superlike') AND NOT EXISTS(SELECT 1 FROM squad_members sm JOIN squads s ON s.id=sm.squad_id WHERE s.event_id=es.event_id AND s.status<>'archived' AND s.type='event_match' AND sm.user_id=es.user_id) ORDER BY es.created_at,es.id LIMIT 1 FOR UPDATE`, swipe.EventID, swipe.UserID).Scan(&otherID, &otherUser)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	squadID, chatID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO squads(id,event_id,name,status,chat_room_id,created_at,expires_at,type) VALUES($1,$2,'New Crew','forming',$3,$4,$5,'event_match')`, squadID, swipe.EventID, chatID, now, now.Add(24*time.Hour)); err != nil {
		return nil, fmt.Errorf("create matched squad: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO squad_members(squad_id,user_id,role,joined_at,has_ticket) VALUES($1,$2,'member',$4,false),($1,$3,'member',$4,false)`, squadID, swipe.UserID, otherUser, now); err != nil {
		return nil, fmt.Errorf("create squad members: %w", err)
	}
	if _, err := ensureSquadChat(ctx, tx, squadID, swipe.UserID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_swipes SET has_crew=true WHERE event_id=$1 AND user_id IN($2,$3) AND has_crew=false`, swipe.EventID, swipe.UserID, otherUser); err != nil {
		return nil, err
	}
	squads, err := queryMatcherSquads(ctx, tx, `WHERE s.id=$1`, squadID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &squads[0], nil
}

func (r *MatchRepository) EnsureSquadChat(ctx context.Context, squadID, userID string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	chatID, err := ensureSquadChat(ctx, tx, squadID, userID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return chatID, nil
}

func ensureSquadChat(ctx context.Context, tx *sql.Tx, squadID, userID string) (string, error) {
	var chat sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT chat_room_id FROM squads WHERE id=$1 FOR UPDATE`, squadID).Scan(&chat); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrSquadNotFound
		}
		return "", err
	}
	var member bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squad_members WHERE squad_id=$1 AND user_id=$2)`, squadID, userID).Scan(&member); err != nil {
		return "", err
	}
	if !member {
		return "", domain.ErrSquadForbidden
	}
	chatID := chat.String
	if chatID == "" {
		chatID = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `UPDATE squads SET chat_room_id=$2 WHERE id=$1`, squadID, chatID); err != nil {
			return "", err
		}
	}
	// Never adopt another squad's conversation, a DIRECT chat, or outsiders.
	var incompatible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squads WHERE chat_room_id=$1 AND id<>$2) OR EXISTS(SELECT 1 FROM chats WHERE id=$1 AND type<>'CREWS') OR EXISTS(SELECT 1 FROM chat_participants cp WHERE cp.chat_id=$1 AND NOT EXISTS(SELECT 1 FROM squad_members sm WHERE sm.squad_id=$2 AND sm.user_id=cp.user_id))`, chatID, squadID).Scan(&incompatible); err != nil {
		return "", err
	}
	if incompatible {
		return "", domain.ErrSquadChatConflict
	}
	created, err := tx.ExecContext(ctx, `INSERT INTO chats(id,type,last_message,created_at,updated_at) VALUES($1,'CREWS','',NOW(),NOW()) ON CONFLICT(id) DO NOTHING`, chatID)
	if err != nil {
		return "", fmt.Errorf("create squad chat: %w", err)
	}
	added, err := tx.ExecContext(ctx, `INSERT INTO chat_participants(chat_id,user_id,joined_at) SELECT $1,user_id,COALESCE(joined_at,NOW()) FROM squad_members WHERE squad_id=$2 ON CONFLICT DO NOTHING`, chatID, squadID)
	if err != nil {
		return "", fmt.Errorf("add squad chat participants: %w", err)
	}
	createdCount, err := created.RowsAffected()
	if err != nil {
		return "", err
	}
	addedCount, err := added.RowsAffected()
	if err != nil {
		return "", err
	}
	if createdCount+addedCount > 0 {
		if _, err := tx.ExecContext(ctx, `SELECT pg_notify('root_chat_events',jsonb_build_object('type','chat.created','chat_id',$1::text)::text)`, chatID); err != nil {
			return "", err
		}
	}
	return chatID, nil
}

type MatchRepository struct {
	db *sql.DB
}

func NewMatchRepository(db *sql.DB) *MatchRepository {
	return &MatchRepository{db: db}
}

func (r *MatchRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE INDEX IF NOT EXISTS squads_chat_room_idx ON squads(chat_room_id) WHERE chat_room_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS squad_members_user_squad_idx ON squad_members(user_id,squad_id)`,
		`CREATE INDEX IF NOT EXISTS event_swipes_pending_event_idx ON event_swipes(event_id,created_at,id) WHERE has_crew=false`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("matcher chat indexes: %w", err)
		}
	}
	return tx.Commit()
}
