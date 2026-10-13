package postgres

import (
	"context"
	"database/sql"
	"time"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type danceRepository struct {
	db *sql.DB
}

func NewDanceRepository(db *sql.DB) ports.DanceRepository {
	return &danceRepository{db: db}
}

func (r *danceRepository) SaveDanceSession(ctx context.Context, session *domain.DanceSession) error {
	upsertQuery := `
		INSERT INTO dance_sessions (id, user_id, event_id, steps_count, start_time, end_time, is_validated, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, event_id) 
		DO UPDATE SET 
			steps_count = dance_sessions.steps_count + EXCLUDED.steps_count,
			end_time = EXCLUDED.end_time,
			is_validated = EXCLUDED.is_validated
	`
	_, err := r.db.ExecContext(ctx, upsertQuery, 
		session.ID, session.UserID, session.EventID, session.StepsCount, 
		session.StartTime, session.EndTime, session.IsValidated, session.CreatedAt)
	
	return err
}

func (r *danceRepository) GetCrewLeaderboardAllTime(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	query := `
		SELECT u.id, u.name, u.username, u.avatar_url, SUM(ds.steps_count) as total_steps
		FROM squad_members sm
		JOIN users u ON sm.user_id = u.id
		JOIN dance_sessions ds ON u.id = ds.user_id
		WHERE sm.squad_id = $1 AND ds.is_validated = true
		GROUP BY u.id, u.name, u.username, u.avatar_url
		ORDER BY total_steps DESC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leaderboard []map[string]interface{}
	for rows.Next() {
		var id, name, username string
		var avatarURL *string
		var totalSteps int
		if err := rows.Scan(&id, &name, &username, &avatarURL, &totalSteps); err != nil {
			return nil, err
		}
		leaderboard = append(leaderboard, map[string]interface{}{
			"userId":    id,
			"name":      name,
			"username":  username,
			"avatarUrl": avatarURL,
			"steps":     totalSteps,
		})
	}
	return leaderboard, nil
}

func (r *danceRepository) GetCrewLeaderboardByEvent(ctx context.Context, squadID, eventID string) ([]map[string]interface{}, error) {
	query := `
		SELECT u.id, u.name, u.username, u.avatar_url, SUM(ds.steps_count) as total_steps
		FROM squad_members sm
		JOIN users u ON sm.user_id = u.id
		JOIN dance_sessions ds ON u.id = ds.user_id
		WHERE sm.squad_id = $1 AND ds.event_id = $2 AND ds.is_validated = true
		GROUP BY u.id, u.name, u.username, u.avatar_url
		ORDER BY total_steps DESC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leaderboard []map[string]interface{}
	for rows.Next() {
		var id, name, username string
		var avatarURL *string
		var totalSteps int
		if err := rows.Scan(&id, &name, &username, &avatarURL, &totalSteps); err != nil {
			return nil, err
		}
		leaderboard = append(leaderboard, map[string]interface{}{
			"userId":    id,
			"name":      name,
			"username":  username,
			"avatarUrl": avatarURL,
			"steps":     totalSteps,
		})
	}
	return leaderboard, nil
}

func (r *danceRepository) GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	query := `
		WITH crew_steps AS (
			SELECT sm.squad_id, ds.user_id, COALESCE(SUM(ds.steps_count), 0) as total_steps
			FROM squad_members sm
			LEFT JOIN dance_sessions ds ON ds.user_id = sm.user_id AND ds.is_validated = true
			GROUP BY sm.squad_id, ds.user_id
		),
		crew_ranks AS (
			SELECT squad_id, user_id, total_steps,
				RANK() OVER (PARTITION BY squad_id ORDER BY total_steps DESC) as rank
			FROM crew_steps
		)
		SELECT s.id, s.name, s.type, s.invite_code, s.status,
		       (SELECT COUNT(*) FROM squad_members WHERE squad_id = s.id) as member_count,
		       COALESCE(cr.total_steps, 0) as top_steps,
		       COALESCE(cr.rank, 0) as user_rank
		FROM squads s
		JOIN squad_members sm ON sm.squad_id = s.id AND sm.user_id = $1
		LEFT JOIN crew_ranks cr ON cr.squad_id = s.id AND cr.user_id = $1
		WHERE s.type = 'permanent'
		ORDER BY s.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var crews []map[string]interface{}
	for rows.Next() {
		var id, name, crewType, status string
		var inviteCode *string
		var memberCount, topSteps, userRank int
		if err := rows.Scan(&id, &name, &crewType, &inviteCode, &status, &memberCount, &topSteps, &userRank); err != nil {
			return nil, err
		}
		crews = append(crews, map[string]interface{}{
			"id":          id,
			"name":        name,
			"type":        crewType,
			"inviteCode":  inviteCode,
			"status":      status,
			"memberCount": memberCount,
			"topSteps":    topSteps,
			"userRank":    userRank,
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
	query := `
		SELECT s.id, s.name, s.type, s.invite_code, s.status,
		       COUNT(sm.user_id) as member_count
		FROM squads s
		LEFT JOIN squad_members sm ON sm.squad_id = s.id
		WHERE s.id = $1
		GROUP BY s.id, s.name, s.type, s.invite_code, s.status
	`
	var id, name, crewType, status string
	var inviteCode *string
	var memberCount int
	err := r.db.QueryRowContext(ctx, query, squadID).Scan(&id, &name, &crewType, &inviteCode, &status, &memberCount)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"id":          id,
		"name":        name,
		"type":        crewType,
		"inviteCode":  inviteCode,
		"status":      status,
		"memberCount": memberCount,
	}, nil
}

func (r *danceRepository) GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	query := `
		SELECT DISTINCT e.id, e.title, e.date, e.location, e.price, e.cinematic_banner_url
		FROM events e
		JOIN dance_sessions ds ON e.id = ds.event_id
		JOIN squad_members sm ON ds.user_id = sm.user_id
		WHERE sm.squad_id = $1 AND ds.is_validated = true
		ORDER BY e.date DESC
	`
	rows, err := r.db.QueryContext(ctx, query, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id, title, location string
		var date time.Time
		var price *float64
		var cinematicBannerURL *string
		if err := rows.Scan(&id, &title, &date, &location, &price, &cinematicBannerURL); err != nil {
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
			"goingCount":         0,
		})
	}
	return events, nil
}
