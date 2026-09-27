package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"root-backend-service/internal/core/domain"
)

type MatchRepository struct {
	db *sql.DB
}

func NewMatchRepository(db *sql.DB) *MatchRepository {
	return &MatchRepository{db: db}
}

func (r *MatchRepository) CreateSwipe(ctx context.Context, swipe *domain.EventSwipe) error {
	prefBytes, err := json.Marshal(swipe.Preferences)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO event_swipes (id, user_id, event_id, direction, has_crew, preferences, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, swipe.ID, swipe.UserID, swipe.EventID, swipe.Direction, swipe.HasCrew, prefBytes, swipe.CreatedAt)
	return err
}

func (r *MatchRepository) GetPendingSwipes(ctx context.Context, eventID string) ([]*domain.EventSwipe, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, event_id, direction, has_crew, preferences, created_at
		FROM event_swipes
		WHERE event_id = $1 AND has_crew = false
	`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var swipes []*domain.EventSwipe
	for rows.Next() {
		s := &domain.EventSwipe{}
		var prefBytes []byte
		if err := rows.Scan(&s.ID, &s.UserID, &s.EventID, &s.Direction, &s.HasCrew, &prefBytes, &s.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(prefBytes, &s.Preferences); err != nil {
			return nil, err
		}
		swipes = append(swipes, s)
	}
	return swipes, nil
}

func (r *MatchRepository) UpdateSwipeHasCrew(ctx context.Context, swipeID string, hasCrew bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE event_swipes SET has_crew = $1 WHERE id = $2`, hasCrew, swipeID)
	return err
}

func (r *MatchRepository) CreateSquad(ctx context.Context, squad *domain.Squad) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO squads (id, event_id, name, status, chat_room_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, squad.ID, squad.EventID, squad.Name, squad.Status, squad.ChatRoomID, squad.CreatedAt, squad.ExpiresAt)
	return err
}

func (r *MatchRepository) AddSquadMember(ctx context.Context, member *domain.SquadMember) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO squad_members (squad_id, user_id, role, joined_at, has_ticket)
		VALUES ($1, $2, $3, $4, $5)
	`, member.SquadID, member.UserID, member.Role, member.JoinedAt, member.HasTicket)
	return err
}
