package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// "Mandanos tu evento": tabla `event_submissions`. Publicar crea la fila en `events`.

type EventSubmissionRepository struct{ db *sql.DB }

var _ ports.EventSubmissionRepository = (*EventSubmissionRepository)(nil)

func NewEventSubmissionRepository(db *sql.DB) *EventSubmissionRepository {
	return &EventSubmissionRepository{db: db}
}

func (r *EventSubmissionRepository) InitSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning event submissions schema migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_event_submissions_schema', 0))`,
		`CREATE TABLE IF NOT EXISTS event_submissions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 submitter_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 starts_at TIMESTAMPTZ NOT NULL,
 venue_name TEXT NOT NULL CHECK (char_length(venue_name) BETWEEN 1 AND 120),
 venue_address TEXT NOT NULL CHECK (char_length(venue_address) BETWEEN 1 AND 200),
 flyer_key TEXT NOT NULL,
 ticket_url TEXT NULL CHECK (char_length(ticket_url) <= 500),
 lineup TEXT NULL CHECK (char_length(lineup) <= 300),
 genres TEXT[] NOT NULL DEFAULT '{}',
 submitter_role TEXT NOT NULL CHECK (submitter_role IN ('organizer', 'rrpp', 'fan')),
 contact TEXT NOT NULL CHECK (char_length(contact) BETWEEN 1 AND 120),
 rights_confirmed BOOLEAN NOT NULL CHECK (rights_confirmed),
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'rejected')),
 reject_reason TEXT NULL CHECK (char_length(reject_reason) <= 280),
 published_event_id UUID NULL REFERENCES events(id) ON DELETE SET NULL,
 reviewed_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
 reviewed_at TIMESTAMPTZ NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE INDEX IF NOT EXISTS event_submissions_submitter_created_idx ON event_submissions (submitter_user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS event_submissions_status_created_idx ON event_submissions (status, created_at)`,
		`CREATE INDEX IF NOT EXISTS event_submissions_published_event_idx ON event_submissions (published_event_id) WHERE published_event_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS event_submissions_reviewed_by_idx ON event_submissions (reviewed_by) WHERE reviewed_by IS NOT NULL`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("event submissions migration: %w", err)
		}
	}
	return tx.Commit()
}

const submissionColumns = `s.id, s.name, s.starts_at, s.venue_name, s.venue_address, s.flyer_key, s.ticket_url, s.lineup,
 s.genres, s.submitter_role, s.status, s.reject_reason, s.published_event_id, s.created_at`

func scanSubmission(scan func(...interface{}) error, extra ...interface{}) (domain.EventSubmission, error) {
	var (
		s                                          domain.EventSubmission
		flyer                                      string
		ticketURL, lineup, rejectReason, publishID sql.NullString
		genres                                     pq.StringArray
	)
	dest := append([]interface{}{&s.ID, &s.Name, &s.StartsAt, &s.VenueName, &s.VenueAddress, &flyer, &ticketURL, &lineup,
		&genres, &s.SubmitterRole, &s.Status, &rejectReason, &publishID, &s.CreatedAt}, extra...)
	if err := scan(dest...); err != nil {
		return s, err
	}
	str := func(v sql.NullString) *string {
		if !v.Valid {
			return nil
		}
		value := v.String
		return &value
	}
	s.FlyerURL = &flyer
	s.TicketURL, s.Lineup, s.RejectReason, s.EventID = str(ticketURL), str(lineup), str(rejectReason), str(publishID)
	s.Genres = []string(genres)
	if s.Genres == nil {
		s.Genres = []string{}
	}
	return s, nil
}

func (r *EventSubmissionRepository) Create(ctx context.Context, userID string, in domain.EventSubmissionInput) (domain.EventSubmission, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.EventSubmission{}, fmt.Errorf("begin create event submission: %w", err)
	}
	defer tx.Rollback()
	// Serializa los envíos de la misma persona: el tope de pendientes no se pasa con pedidos simultáneos.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('root_event_submission:' || $1::text, 0))`, userID); err != nil {
		return domain.EventSubmission{}, fmt.Errorf("locking submitter: %w", err)
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_submissions WHERE submitter_user_id = $1::uuid AND status = 'pending'`, userID).Scan(&pending); err != nil {
		return domain.EventSubmission{}, fmt.Errorf("counting pending submissions: %w", err)
	}
	if pending >= domain.SubmissionsMaxPending {
		return domain.EventSubmission{}, domain.ErrTooManyPendingSubmissions
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO event_submissions AS s (id, submitter_user_id, name, starts_at, venue_name, venue_address,
 flyer_key, ticket_url, lineup, genres, submitter_role, contact, rights_confirmed, status)
 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, TRUE, 'pending') RETURNING `+submissionColumns,
		uuid.NewString(), userID, in.Name, in.StartsAt, in.VenueName, in.VenueAddress, in.FlyerKey, nullableString(in.TicketURL),
		nullableString(in.Lineup), genresArray(in.Genres), in.SubmitterRole, in.Contact)
	created, err := scanSubmission(row.Scan)
	if err != nil {
		return created, fmt.Errorf("inserting event submission: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return created, fmt.Errorf("commit create event submission: %w", err)
	}
	return created, nil
}

func (r *EventSubmissionRepository) ListMine(ctx context.Context, userID string) ([]domain.EventSubmission, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+submissionColumns+` FROM event_submissions s
 WHERE s.submitter_user_id = $1::uuid ORDER BY s.created_at DESC, s.id LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing my event submissions: %w", err)
	}
	defer rows.Close()
	out := make([]domain.EventSubmission, 0)
	for rows.Next() {
		item, err := scanSubmission(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning event submission: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *EventSubmissionRepository) AdminList(ctx context.Context, status string, limit, offset int) ([]domain.AdminEventSubmission, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+submissionColumns+`, s.contact, u.id, COALESCE(u.name, ''), COALESCE(u.username, ''),
 NULLIF(u.avatar_url, '')
 FROM event_submissions s JOIN users u ON u.id = s.submitter_user_id
 WHERE s.status = $1 ORDER BY s.created_at ASC, s.id LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing event submissions for review: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AdminEventSubmission, 0)
	for rows.Next() {
		var item domain.AdminEventSubmission
		var avatar sql.NullString
		sub, err := scanSubmission(rows.Scan, &item.Contact, &item.Submitter.UserID, &item.Submitter.Name, &item.Submitter.Username, &avatar)
		if err != nil {
			return nil, fmt.Errorf("scanning event submission: %w", err)
		}
		item.EventSubmission = sub
		if avatar.Valid {
			item.Submitter.AvatarURL = &avatar.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type lockedSubmission struct {
	userID, name, venue, address, flyer, status string
	startsAt                                    time.Time
	ticketURL, lineup                           sql.NullString
	genres                                      pq.StringArray
}

func lockSubmission(ctx context.Context, tx *sql.Tx, id string) (lockedSubmission, error) {
	var s lockedSubmission
	err := tx.QueryRowContext(ctx, `SELECT submitter_user_id, name, venue_name, venue_address, flyer_key, status, starts_at,
 ticket_url, lineup, genres FROM event_submissions WHERE id = $1::uuid FOR UPDATE`, id).Scan(&s.userID, &s.name, &s.venue,
		&s.address, &s.flyer, &s.status, &s.startsAt, &s.ticketURL, &s.lineup, &s.genres)
	if errors.Is(err, sql.ErrNoRows) {
		return s, domain.ErrSubmissionNotFound
	}
	if err != nil {
		return s, fmt.Errorf("locking event submission: %w", err)
	}
	if s.status != domain.SubmissionPending {
		return s, domain.ErrSubmissionNotPending
	}
	return s, nil
}

// splitLineup convierte "A, B / C" en ["A","B","C"] para events.lineup.
func splitLineup(lineup string) []string {
	parts := strings.FieldsFunc(lineup, func(r rune) bool { return r == ',' || r == '\n' || r == '/' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func submissionPushData(id string, extra map[string]string) map[string]string {
	data := map[string]string{"id": id, "submission_id": id, "url": "/events/submissions"}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

// Publish crea el evento. Decisiones: events.date guarda la hora de Montevideo sin zona (como el resto de los eventos
// y como lo lee el front); el lugar va en
// location y la dirección en la descripción; producer_id queda vacío (no se le da al remitente el rango de
// organizador sin verificarlo); el país es el del remitente o UY.
func (r *EventSubmissionRepository) Publish(ctx context.Context, submissionID, adminID string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin publish event submission: %w", err)
	}
	defer tx.Rollback()
	s, err := lockSubmission(ctx, tx, submissionID)
	if err != nil {
		return "", err
	}
	var genre interface{}
	if len(s.genres) > 0 {
		genre = truncateRunes(strings.Join(s.genres, ", "), 100)
	}
	lineup := []string{}
	if s.lineup.Valid {
		lineup = splitLineup(s.lineup.String)
	}
	var ticketURL interface{}
	if s.ticketURL.Valid {
		ticketURL = s.ticketURL.String
	}
	var eventID string
	if err := tx.QueryRowContext(ctx, `INSERT INTO events (title, date, location, country, cinematic_banner_url, description,
 lineup, genre, ticket_url, is_featured, created_at)
 VALUES ($1, ($2::timestamptz AT TIME ZONE 'America/Montevideo'), $3,
  COALESCE((SELECT NULLIF(TRIM(country), '') FROM users WHERE id = $4::uuid), 'UY'), $5, $6, $7, $8, $9, FALSE, NOW())
 RETURNING id::text`, s.name, s.startsAt, s.venue, s.userID, s.flyer, "Dirección: "+s.address, pq.Array(lineup), genre, ticketURL).Scan(&eventID); err != nil {
		return "", fmt.Errorf("creating event from submission: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_submissions SET status = 'published', published_event_id = $2::uuid,
 reviewed_by = $3::uuid, reviewed_at = NOW(), reject_reason = NULL WHERE id = $1::uuid`, submissionID, eventID, adminID); err != nil {
		return "", fmt.Errorf("marking submission published: %w", err)
	}
	if err := afterNotify(ctx, tx, s.userID, "event_submission.published", domain.NotificationEvents, "¡Publicamos tu evento!",
		fmt.Sprintf("%s ya está en root.", s.name), "event_submission.published:"+submissionID,
		submissionPushData(submissionID, map[string]string{"event_id": eventID})); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit publish event submission: %w", err)
	}
	return eventID, nil
}

func (r *EventSubmissionRepository) Reject(ctx context.Context, submissionID, adminID, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reject event submission: %w", err)
	}
	defer tx.Rollback()
	s, err := lockSubmission(ctx, tx, submissionID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_submissions SET status = 'rejected', reject_reason = $2, reviewed_by = $3::uuid,
 reviewed_at = NOW() WHERE id = $1::uuid`, submissionID, reason, adminID); err != nil {
		return fmt.Errorf("rejecting submission: %w", err)
	}
	if err := afterNotify(ctx, tx, s.userID, "event_submission.rejected", domain.NotificationEvents, "No pudimos publicar tu evento",
		fmt.Sprintf("Revisamos %s. Tocá para ver el motivo.", s.name), "event_submission.rejected:"+submissionID,
		submissionPushData(submissionID, nil)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reject event submission: %w", err)
	}
	return nil
}

// Similar: cada palabra del nombre (≥ 3 letras, hasta 4) tiene que aparecer en el título. Con fecha, ±36 h; sin
// fecha, eventos que no terminaron hace más de un día. Ordena por cercanía a la fecha pedida.
func (r *EventSubmissionRepository) Similar(ctx context.Context, name string, date *time.Time, limit int) ([]domain.Event, error) {
	words := make([]string, 0, 4)
	for _, word := range strings.Fields(name) {
		if len([]rune(word)) >= 3 {
			words = append(words, word)
		}
		if len(words) == 4 {
			break
		}
	}
	if len(words) == 0 {
		words = append(words, strings.TrimSpace(name))
	}
	args := []interface{}{anonymousUserID}
	clauses := make([]string, 0, len(words)+1)
	for _, word := range words {
		args = append(args, "%"+escapeLike(word)+"%")
		clauses = append(clauses, fmt.Sprintf("e.title ILIKE $%d", len(args)))
	}
	order := "e.date ASC"
	if date != nil {
		args = append(args, *date)
		n := len(args)
		clauses = append(clauses, fmt.Sprintf(`(e.date AT TIME ZONE 'America/Montevideo') BETWEEN $%d::timestamptz - INTERVAL '36 hours' AND $%d::timestamptz + INTERVAL '36 hours'`, n, n))
		order = fmt.Sprintf(`ABS(EXTRACT(EPOCH FROM ((e.date AT TIME ZONE 'America/Montevideo') - $%d::timestamptz)))`, n)
	} else {
		clauses = append(clauses, `COALESCE(e.end_date, (e.date AT TIME ZONE 'America/Montevideo') + INTERVAL '12 hours') >= NOW() - INTERVAL '1 day'`)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.title, e.producer_id, e.date, e.end_date, e.location, e.latitude, e.longitude,
			COALESCE(e.cinematic_banner_url, ''), COALESCE(e.description, ''),
			COALESCE(e.lineup, '{}'), e.genre, e.price,
			COALESCE(e.is_featured, false), COALESCE(e.created_at, NOW()),
			COALESCE(counts.going_count, 0), COALESCE(counts.not_going_count, 0), ur.status,
			e.ticket_tiers, e.ticket_source, e.ticket_url, e.ticket_info_fetched_at, e.slug
		FROM events e
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE status = 'going') AS going_count,
				COUNT(*) FILTER (WHERE status = 'not_going') AS not_going_count
			FROM event_rsvps WHERE event_id = e.id
		) counts ON TRUE
		LEFT JOIN event_rsvps ur ON ur.event_id = e.id AND ur.user_id::text = $1
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY `+order+`, e.id LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("searching similar events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}
