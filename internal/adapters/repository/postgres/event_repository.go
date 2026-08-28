package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"

	"github.com/lib/pq"
)

const anonymousUserID = "00000000-0000-0000-0000-000000000000"

type EventRepository struct {
	db *sql.DB
}

func NewEventRepository(db *sql.DB) ports.EventRepository {
	return &EventRepository{db: db}
}

// InitSchema applies all event schema changes atomically. Returning failures
// prevents the API from starting with only part of the required schema.
func (r *EventRepository) InitSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning event schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS genre VARCHAR(100)`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS price NUMERIC(10,2)`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'events_price_non_negative' AND conrelid = 'events'::regclass
			) THEN
				ALTER TABLE events ADD CONSTRAINT events_price_non_negative CHECK (price IS NULL OR price >= 0);
			END IF;
		END $$`,
		`DELETE FROM event_rsvps
		WHERE ctid IN (
			SELECT duplicate_ctid FROM (
				SELECT ctid AS duplicate_ctid,
					ROW_NUMBER() OVER (
						PARTITION BY user_id, event_id
						ORDER BY created_at DESC NULLS LAST, ctid DESC
					) AS position
				FROM event_rsvps
			) ranked
			WHERE position > 1
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS event_rsvps_user_event_uidx ON event_rsvps (user_id, event_id)`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'event_rsvps_status_check' AND conrelid = 'event_rsvps'::regclass
			) THEN
				ALTER TABLE event_rsvps ADD CONSTRAINT event_rsvps_status_check CHECK (status IN ('going', 'not_going'));
			END IF;
		END $$`,
		`CREATE INDEX IF NOT EXISTS events_upcoming_date_idx ON events (date, id)`,
		`CREATE INDEX IF NOT EXISTS event_rsvps_event_status_idx ON event_rsvps (event_id, status)`,
		`CREATE INDEX IF NOT EXISTS event_comments_target_time_idx
			ON comments (target_id, timestamp DESC, id DESC)
			WHERE target_type = 'event'`,
	}

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("applying event schema migration: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing event schema migration: %w", err)
	}
	return nil
}

func (r *EventRepository) GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error) {
	isFeatured := true
	filter := domain.EventFilter{FeaturedOnly: &isFeatured, Location: country, Limit: 20}
	events, _, err := r.GetEvents(ctx, filter, "")
	return events, err
}

func buildEventWhere(filter domain.EventFilter, startArg int) (string, []interface{}) {
	clauses := []string{"e.date >= NOW()"}
	args := make([]interface{}, 0, 8)
	argIdx := startArg

	add := func(clause string, value interface{}) {
		clauses = append(clauses, fmt.Sprintf(clause, argIdx))
		args = append(args, value)
		argIdx++
	}

	if filter.FeaturedOnly != nil {
		add("e.is_featured = $%d", *filter.FeaturedOnly)
	}
	if filter.Genre != "" && !strings.EqualFold(filter.Genre, "all") && !strings.EqualFold(filter.Genre, "todos") {
		add("e.genre ILIKE $%d", "%"+filter.Genre+"%")
	}
	if filter.Location != "" && !strings.EqualFold(filter.Location, "all") {
		add("e.location ILIKE $%d", "%"+filter.Location+"%")
	}
	if filter.MinPrice != nil {
		add("e.price >= $%d", *filter.MinPrice)
	}
	if filter.MaxPrice != nil {
		add("e.price <= $%d", *filter.MaxPrice)
	}
	if filter.IsFree != nil {
		if *filter.IsFree {
			clauses = append(clauses, "e.price = 0")
		} else {
			clauses = append(clauses, "e.price > 0")
		}
	}
	if filter.StartDate != nil {
		add("e.date >= $%d", *filter.StartDate)
	}
	if filter.EndDate != nil {
		add("e.date <= $%d", *filter.EndDate)
	}
	if filter.Query != "" {
		placeholder := fmt.Sprintf("$%d", argIdx)
		clauses = append(clauses, "(e.title ILIKE "+placeholder+" OR e.description ILIKE "+placeholder+" OR e.location ILIKE "+placeholder+")")
		args = append(args, "%"+filter.Query+"%")
	}

	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (r *EventRepository) GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error) {
	if currentUserID == "" {
		currentUserID = anonymousUserID
	}
	if filter.Limit <= 0 {
		filter.Limit = 12
	}
	if filter.Limit > 50 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	countWhere, countArgs := buildEventWhere(filter, 1)
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events e`+countWhere, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting filtered events: %w", err)
	}

	where, filterArgs := buildEventWhere(filter, 2)
	query := `
		SELECT e.id, e.title, e.producer_id, e.date, e.location,
			COALESCE(e.cinematic_banner_url, ''), COALESCE(e.description, ''),
			COALESCE(e.lineup, '{}'), e.genre, e.price,
			COALESCE(e.is_featured, false), COALESCE(e.created_at, NOW()),
			COALESCE(counts.going_count, 0), COALESCE(counts.not_going_count, 0), ur.status
		FROM events e
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE status = 'going') AS going_count,
				COUNT(*) FILTER (WHERE status = 'not_going') AS not_going_count
			FROM event_rsvps WHERE event_id = e.id
		) counts ON TRUE
		LEFT JOIN event_rsvps ur ON ur.event_id = e.id AND ur.user_id::text = $1
	` + where + `
		ORDER BY e.date ASC, e.id ASC
		LIMIT $%d OFFSET $%d`

	args := append([]interface{}{currentUserID}, filterArgs...)
	query = fmt.Sprintf(query, len(args)+1, len(args)+2)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func (r *EventRepository) GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error) {
	if currentUserID == "" {
		currentUserID = anonymousUserID
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.title, e.producer_id, e.date, e.location,
			COALESCE(e.cinematic_banner_url, ''), COALESCE(e.description, ''),
			COALESCE(e.lineup, '{}'), e.genre, e.price,
			COALESCE(e.is_featured, false), COALESCE(e.created_at, NOW()),
			COALESCE(counts.going_count, 0), COALESCE(counts.not_going_count, 0), ur.status
		FROM events e
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE status = 'going') AS going_count,
				COUNT(*) FILTER (WHERE status = 'not_going') AS not_going_count
			FROM event_rsvps WHERE event_id = e.id
		) counts ON TRUE
		LEFT JOIN event_rsvps ur ON ur.event_id = e.id AND ur.user_id::text = $1
		WHERE e.id::text = $2`, currentUserID, id)
	if err != nil {
		return nil, fmt.Errorf("querying event by id: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, sql.ErrNoRows
	}
	return &events[0], nil
}

func (r *EventRepository) RSVPEvent(ctx context.Context, userID, eventID, status string) (int, int, string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", fmt.Errorf("beginning rsvp transaction: %w", err)
	}
	defer tx.Rollback()

	var eventExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id::text = $1)`, eventID).Scan(&eventExists); err != nil {
		return 0, 0, "", fmt.Errorf("checking event before rsvp: %w", err)
	}
	if !eventExists {
		return 0, 0, "", sql.ErrNoRows
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO event_rsvps (user_id, event_id, status, created_at)
		SELECT u.id, e.id, $3, NOW() FROM users u, events e
		WHERE u.id::text = $1 AND e.id::text = $2
		ON CONFLICT (user_id, event_id)
		DO UPDATE SET status = EXCLUDED.status, created_at = NOW()`, userID, eventID, status)
	if err != nil {
		return 0, 0, "", fmt.Errorf("upserting event rsvp: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return 0, 0, "", sql.ErrNoRows
	}

	var goingCount, notGoingCount int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'going'),
			COUNT(*) FILTER (WHERE status = 'not_going')
		FROM event_rsvps WHERE event_id::text = $1`, eventID).Scan(&goingCount, &notGoingCount)
	if err != nil {
		return 0, 0, "", fmt.Errorf("reading rsvp counts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, "", fmt.Errorf("committing rsvp: %w", err)
	}
	return goingCount, notGoingCount, status, nil
}

func (r *EventRepository) GetFollowedGoingAttendees(ctx context.Context, eventID, currentUserID string, limit, offset int) ([]domain.Attendee, int, error) {
	if err := ensureEventExists(ctx, r.db, eventID); err != nil {
		return nil, 0, err
	}

	baseWhere := ` FROM event_rsvps r
		JOIN users u ON r.user_id = u.id
		JOIN users current_user ON current_user.id::text = $2
		WHERE r.event_id::text = $1 AND r.status = 'going'
		  AND u.id::text = ANY(COALESCE(current_user.following, '{}'::text[]))`

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+baseWhere, eventID, currentUserID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting followed event attendees: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT u.id, u.name, u.username, u.avatar_url, COALESCE(u.is_kyc_verified, false)`+
		baseWhere+`
		ORDER BY r.created_at DESC, u.id ASC LIMIT $3 OFFSET $4`, eventID, currentUserID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("querying followed event attendees: %w", err)
	}
	defer rows.Close()

	attendees := make([]domain.Attendee, 0)
	for rows.Next() {
		var attendee domain.Attendee
		var avatarURL sql.NullString
		if err := rows.Scan(&attendee.ID, &attendee.Name, &attendee.Username, &avatarURL, &attendee.IsKycVerified); err != nil {
			return nil, 0, fmt.Errorf("scanning followed attendee: %w", err)
		}
		if avatarURL.Valid {
			attendee.AvatarURL = &avatarURL.String
		}
		attendees = append(attendees, attendee)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating followed attendees: %w", err)
	}
	return attendees, total, nil
}

func (r *EventRepository) GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error) {
	if err := ensureEventExists(ctx, r.db, eventID); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE target_type = 'event' AND target_id::text = $1`, eventID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting event comments: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.target_id, c.author_id,
			COALESCE(u.name, 'Usuario'), COALESCE(u.username, 'usuario'),
			u.avatar_url, c.content, COALESCE(c.timestamp, NOW())
		FROM comments c LEFT JOIN users u ON c.author_id = u.id
		WHERE c.target_type = 'event' AND c.target_id::text = $1
		ORDER BY c.timestamp DESC, c.id DESC LIMIT $2 OFFSET $3`, eventID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("querying event comments: %w", err)
	}
	defer rows.Close()

	comments := make([]domain.EventComment, 0)
	for rows.Next() {
		var comment domain.EventComment
		var avatarURL, targetID, authorID sql.NullString
		if err := rows.Scan(
			&comment.ID, &targetID, &authorID, &comment.AuthorName,
			&comment.AuthorUsername, &avatarURL, &comment.Content, &comment.Timestamp,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning event comment: %w", err)
		}
		if targetID.Valid {
			comment.TargetID = targetID.String
		}
		if authorID.Valid {
			comment.AuthorID = authorID.String
		}
		if avatarURL.Valid {
			comment.AuthorAvatar = &avatarURL.String
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating event comments: %w", err)
	}
	return comments, total, nil
}

func (r *EventRepository) CreateEventComment(ctx context.Context, eventID, authorID, content string) (*domain.EventComment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning event comment transaction: %w", err)
	}
	defer tx.Rollback()

	if err := ensureEventExists(ctx, tx, eventID); err != nil {
		return nil, err
	}

	comment := &domain.EventComment{TargetID: eventID, AuthorID: authorID, Content: content}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO comments (id, target_type, target_id, author_id, content, timestamp)
		SELECT gen_random_uuid(), 'event', $1, u.id, $3, NOW()
		FROM events e, users u WHERE e.id::text = $1 AND u.id::text = $2
		RETURNING id, timestamp`, eventID, authorID, content).Scan(&comment.ID, &comment.Timestamp); err != nil {
		return nil, fmt.Errorf("creating event comment: %w", err)
	}

	var avatarURL sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT name, username, avatar_url FROM users WHERE id::text = $1`, authorID).Scan(
		&comment.AuthorName, &comment.AuthorUsername, &avatarURL,
	); err != nil {
		return nil, fmt.Errorf("reading event comment author: %w", err)
	}
	if avatarURL.Valid {
		comment.AuthorAvatar = &avatarURL.String
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing event comment: %w", err)
	}
	return comment, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func ensureEventExists(ctx context.Context, db queryRower, eventID string) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id::text = $1)`, eventID).Scan(&exists); err != nil {
		return fmt.Errorf("checking event existence: %w", err)
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func (r *EventRepository) SearchEvents(ctx context.Context, query string) ([]domain.Event, error) {
	filter := domain.EventFilter{Query: query, Limit: 20}
	events, _, err := r.GetEvents(ctx, filter, "")
	return events, err
}

func scanEvents(rows *sql.Rows) ([]domain.Event, error) {
	events := make([]domain.Event, 0)
	for rows.Next() {
		var event domain.Event
		var producerID, genre, userRSVP sql.NullString
		var price sql.NullFloat64
		var lineup pq.StringArray

		if err := rows.Scan(
			&event.ID, &event.Title, &producerID, &event.Date, &event.Location,
			&event.CinematicBannerURL, &event.Description, &lineup, &genre, &price,
			&event.IsFeatured, &event.CreatedAt, &event.GoingCount, &event.NotGoingCount, &userRSVP,
		); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}

		if producerID.Valid {
			event.ProducerID = &producerID.String
		}
		if genre.Valid && strings.TrimSpace(genre.String) != "" {
			event.Genre = &genre.String
		}
		if price.Valid {
			event.Price = &price.Float64
			event.IsFree = price.Float64 == 0
		}
		if userRSVP.Valid {
			event.UserRSVP = &userRSVP.String
		}
		event.Lineup = lineup
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}
