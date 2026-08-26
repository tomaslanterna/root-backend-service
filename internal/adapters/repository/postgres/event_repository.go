package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"

	"github.com/lib/pq"
)

type EventRepository struct {
	db *sql.DB
}

func NewEventRepository(db *sql.DB) ports.EventRepository {
	return &EventRepository{db: db}
}

const eventBaseSelect = `
	SELECT 
		e.id, 
		e.title, 
		e.producer_id, 
		e.date, 
		e.location, 
		COALESCE(e.cinematic_banner_url, '') AS cinematic_banner_url, 
		COALESCE(e.description, '') AS description, 
		COALESCE(e.lineup, '{}') AS lineup, 
		COALESCE(e.is_featured, false) AS is_featured, 
		COALESCE(e.created_at, NOW()) AS created_at,
		COALESCE(COUNT(CASE WHEN r.status = 'going' THEN 1 END), 0) AS going_count,
		COALESCE(COUNT(CASE WHEN r.status = 'not_going' THEN 1 END), 0) AS not_going_count
	FROM events e
	LEFT JOIN event_rsvps r ON e.id = r.event_id
`

func (r *EventRepository) GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error) {
	query := eventBaseSelect + ` WHERE e.is_featured = true `
	var args []interface{}

	if country != "" && country != "all" {
		query += ` AND (e.location ILIKE $1 OR $1 = '') `
		args = append(args, "%"+country+"%")
	}

	query += ` GROUP BY e.id ORDER BY e.date ASC `

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error querying featured events: %w", err)
	}
	defer rows.Close()

	return r.scanEvents(rows)
}

func (r *EventRepository) GetEvents(ctx context.Context, featuredOnly *bool, country string) ([]domain.Event, error) {
	query := eventBaseSelect + ` WHERE 1=1 `
	var args []interface{}
	argIdx := 1

	if featuredOnly != nil {
		query += fmt.Sprintf(` AND e.is_featured = $%d `, argIdx)
		args = append(args, *featuredOnly)
		argIdx++
	}

	if country != "" && country != "all" {
		query += fmt.Sprintf(` AND e.location ILIKE $%d `, argIdx)
		args = append(args, "%"+country+"%")
		argIdx++
	}

	query += ` GROUP BY e.id ORDER BY e.date ASC `

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error querying events: %w", err)
	}
	defer rows.Close()

	return r.scanEvents(rows)
}

func (r *EventRepository) GetEventByID(ctx context.Context, id string) (*domain.Event, error) {
	query := eventBaseSelect + ` WHERE e.id = $1 GROUP BY e.id `

	rows, err := r.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("error querying event by id: %w", err)
	}
	defer rows.Close()

	events, err := r.scanEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, sql.ErrNoRows
	}

	return &events[0], nil
}

func (r *EventRepository) RSVPEvent(ctx context.Context, userID, eventID, status string) (int, int, error) {
	// Upsert RSVP
	upsertQuery := `
		INSERT INTO event_rsvps (user_id, event_id, status, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, event_id)
		DO UPDATE SET status = EXCLUDED.status, created_at = NOW()
	`
	_, err := r.db.ExecContext(ctx, upsertQuery, userID, eventID, status)
	if err != nil {
		return 0, 0, fmt.Errorf("error updating rsvp: %w", err)
	}

	// Fetch updated counts
	countQuery := `
		SELECT 
			COALESCE(COUNT(CASE WHEN status = 'going' THEN 1 END), 0) AS going_count,
			COALESCE(COUNT(CASE WHEN status = 'not_going' THEN 1 END), 0) AS not_going_count
		FROM event_rsvps
		WHERE event_id = $1
	`
	var goingCount, notGoingCount int
	err = r.db.QueryRowContext(ctx, countQuery, eventID).Scan(&goingCount, &notGoingCount)
	if err != nil {
		return 0, 0, fmt.Errorf("error reading rsvp counts: %w", err)
	}

	return goingCount, notGoingCount, nil
}

func (r *EventRepository) SearchEvents(ctx context.Context, query string) ([]domain.Event, error) {
	sqlQuery := eventBaseSelect + `
		WHERE e.title ILIKE $1 OR e.description ILIKE $1 OR e.location ILIKE $1
		GROUP BY e.id
		ORDER BY e.date ASC
		LIMIT 20
	`
	searchTerm := "%" + query + "%"
	rows, err := r.db.QueryContext(ctx, sqlQuery, searchTerm)
	if err != nil {
		return nil, fmt.Errorf("error searching events: %w", err)
	}
	defer rows.Close()

	return r.scanEvents(rows)
}

func (r *EventRepository) scanEvents(rows *sql.Rows) ([]domain.Event, error) {
	events := make([]domain.Event, 0)
	for rows.Next() {
		var e domain.Event
		var producerID sql.NullString
		var lineup pq.StringArray
		var date time.Time
		var createdAt time.Time

		err := rows.Scan(
			&e.ID,
			&e.Title,
			&producerID,
			&date,
			&e.Location,
			&e.CinematicBannerURL,
			&e.Description,
			&lineup,
			&e.IsFeatured,
			&createdAt,
			&e.GoingCount,
			&e.NotGoingCount,
		)
		if err != nil {
			return nil, fmt.Errorf("error scanning event row: %w", err)
		}

		if producerID.Valid {
			pid := producerID.String
			e.ProducerID = &pid
		}
		e.Date = date
		e.CreatedAt = createdAt
		e.Lineup = lineup

		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event rows: %w", err)
	}

	return events, nil
}
