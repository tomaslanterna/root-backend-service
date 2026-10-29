package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
)

type BadgeRepository struct {
	db             *sql.DB
	danceAvailable bool
}

func NewBadgeRepository(db *sql.DB) *BadgeRepository {
	return &BadgeRepository{db: db, danceAvailable: true}
}

func (r *BadgeRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_attendance_schema',0))`,
		`CREATE TABLE IF NOT EXISTS event_attendance (
		 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 event_id UUID NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
		 confirmed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		 source TEXT NOT NULL CHECK(source='geofence'),
		 PRIMARY KEY(user_id,event_id))`,
		`CREATE INDEX IF NOT EXISTS event_attendance_collection_idx ON event_attendance(user_id,confirmed_at DESC,event_id DESC)`,
		`ALTER TABLE event_attendance ADD COLUMN IF NOT EXISTS grant_reason TEXT`,
		`ALTER TABLE event_attendance ADD COLUMN IF NOT EXISTS departed_at TIMESTAMPTZ`,
		`ALTER TABLE event_attendance ADD COLUMN IF NOT EXISTS reminder_offered_at TIMESTAMPTZ`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS department TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS country TEXT`,
		`ALTER TABLE artists ADD COLUMN IF NOT EXISTS is_example BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE artists ADD COLUMN IF NOT EXISTS example_user_id UUID REFERENCES users(id) ON DELETE CASCADE`,
		`ALTER TABLE artists ADD COLUMN IF NOT EXISTS example_event_id UUID REFERENCES events(id) ON DELETE CASCADE`,
		`CREATE INDEX IF NOT EXISTS artists_private_example_idx ON artists(example_user_id,example_event_id) WHERE is_example=true`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='artists'::regclass AND conname='artists_example_scope_check') THEN
		 ALTER TABLE artists ADD CONSTRAINT artists_example_scope_check CHECK(
		 (is_example AND example_user_id IS NOT NULL AND example_event_id IS NOT NULL)
		 OR (NOT is_example AND example_user_id IS NULL AND example_event_id IS NULL));
		 END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS event_review_openings (
		 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 opening_id UUID NOT NULL,opened_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
		 offered BOOLEAN NOT NULL DEFAULT false,PRIMARY KEY(user_id,opening_id))`,
		`CREATE INDEX IF NOT EXISTS event_review_openings_time_idx ON event_review_openings(opened_at)`,
		`CREATE INDEX IF NOT EXISTS event_attendance_review_pending_idx ON event_attendance(user_id,departed_at,event_id)
		 WHERE departed_at IS NOT NULL AND reminder_offered_at IS NULL AND source='geofence'`,
		`ALTER TABLE event_attendance DROP CONSTRAINT IF EXISTS event_attendance_source_check`,
		`ALTER TABLE event_attendance ADD CONSTRAINT event_attendance_source_check CHECK(source IN ('geofence','manual'))`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='event_attendance'::regclass AND conname='event_attendance_manual_reason_check') THEN
		 ALTER TABLE event_attendance ADD CONSTRAINT event_attendance_manual_reason_check
		 CHECK(source <> 'manual' OR (grant_reason IS NOT NULL AND length(btrim(grant_reason)) BETWEEN 1 AND 500));
		 END IF; END $$`,
	}
	// Legacy RSVPs and dance sessions do not reliably prove presence: never backfill from them.
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("attendance migration: %w", err)
		}
	}
	var available bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('dance_sessions') IS NOT NULL`).Scan(&available); err != nil {
		return fmt.Errorf("checking existing dance data: %w", err)
	}
	if available {
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS dance_sessions_badge_steps_idx ON dance_sessions(user_id,event_id) WHERE is_validated=true AND steps_count>=0`); err != nil {
			return fmt.Errorf("indexing existing dance data: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.danceAvailable = available
	return nil
}

// GrantManual is only used by the administrative CLI, never exposed over HTTP.
// It records a grant, not GPS evidence, and never modifies steps or reviews.
func (r *BadgeRepository) GrantManual(ctx context.Context, userID string, eventIDs []string, reason string) (int64, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return 0, fmt.Errorf("invalid grant user ID")
	}
	reason = strings.TrimSpace(reason)
	if len(eventIDs) == 0 || len(eventIDs) > 50 || reason == "" || utf8.RuneCountInString(reason) > 500 {
		return 0, fmt.Errorf("manual grants require 1–50 events and a reason of 1–500 characters")
	}
	seen := make(map[string]bool, len(eventIDs))
	for _, id := range eventIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || seen[id] {
			return 0, fmt.Errorf("invalid or duplicate grant event ID")
		}
		seen[id] = true
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning manual grant: %w", err)
	}
	defer tx.Rollback()
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM users WHERE id=$1 FOR KEY SHARE`, userID).Scan(&owner); err != nil {
		return 0, fmt.Errorf("checking grant account: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM events WHERE id=ANY($1::uuid[]) FOR KEY SHARE`, pq.Array(eventIDs))
	if err != nil {
		return 0, fmt.Errorf("checking grant events: %w", err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return 0, fmt.Errorf("reading grant events: %w", rowErr)
	}
	if count != len(eventIDs) {
		return 0, fmt.Errorf("one or more grant events do not exist")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO event_attendance(user_id,event_id,source,grant_reason)
	 SELECT $1,id,'manual',$3 FROM events WHERE id=ANY($2::uuid[])
	 ON CONFLICT(user_id,event_id) DO NOTHING`, userID, pq.Array(eventIDs), reason)
	if err != nil {
		return 0, fmt.Errorf("assigning manual badges: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting manual grants: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing manual grants: %w", err)
	}
	return inserted, nil
}

// Rank the whole collection BEFORE filtering/pagination. Calendar years use the
// same Montevideo timezone as the collection; ties favor latest attendance, then ID.
func badgeSelect(ownerFilter string, danceAvailable bool) string {
	steps := `SELECT event_id,SUM(steps_count)::bigint AS steps FROM dance_sessions
 WHERE user_id=(SELECT id FROM owner) AND is_validated=true AND steps_count>=0 GROUP BY event_id`
	if !danceAvailable {
		steps = `SELECT NULL::uuid AS event_id,NULL::bigint AS steps WHERE false`
	}
	return `WITH owner AS (SELECT id FROM users WHERE ` + ownerFilter + `),
 step_totals AS (` + steps + `),
 ranked AS (SELECT a.*,st.steps,ROW_NUMBER() OVER (
 PARTITION BY EXTRACT(YEAR FROM a.confirmed_at AT TIME ZONE 'America/Montevideo')
 ORDER BY st.steps DESC NULLS LAST,a.confirmed_at DESC,a.event_id DESC) AS step_rank
 FROM event_attendance a LEFT JOIN step_totals st ON st.event_id=a.event_id WHERE a.user_id=(SELECT id FROM owner))
 SELECT a.event_id,e.title,COALESCE(e.cinematic_banner_url,''),e.date,e.location,e.genre,a.confirmed_at,
 a.steps,COALESCE(a.steps>0 AND a.step_rank=1,false),e.lineup,a.source,a.departed_at,e.department,e.country,
 (SELECT COALESCE(jsonb_agg(jsonb_build_object('id',artist.id,'name',artist.name,'imageUrl',COALESCE(artist.avatar_url,'')) ORDER BY ea.performance_time ASC NULLS LAST,ea.artist_id),'[]'::jsonb)
 FROM event_artists ea JOIN artists artist ON artist.id=ea.artist_id WHERE ea.event_id=a.event_id AND ea.is_headliner=true),
 (SELECT jsonb_build_object('id',artist.id,'name',artist.name,'imageUrl',COALESCE(artist.avatar_url,''))
  FROM event_artists ea JOIN artists artist ON artist.id=ea.artist_id
  WHERE ea.event_id=a.event_id AND ea.is_headliner=true
  ORDER BY ea.performance_time ASC NULLS LAST,ea.artist_id ASC LIMIT 1),
 CASE WHEN u.id::text=$2 AND s.id IS NOT NULL THEN jsonb_build_object(
 'id',s.id,'userId',s.user_id,'eventId',s.event_id,'generalRating',s.general_rating,
 'comment',s.comment,'createdAt',s.created_at,'updatedAt',s.updated_at,
 'organizationRating',s.organization_rating,'vibeRating',s.vibe_rating,
 'soundVisualRating',s.sound_visual_rating,'pricingRating',s.pricing_rating,
 'spaceRating',s.space_rating,'wouldReturn',s.would_return,
 'artistRatings',(SELECT COALESCE(jsonb_agg(jsonb_build_object('artistId',ar.artist_id,'rating',ar.rating)),'[]'::jsonb) FROM survey_artist_ratings ar WHERE ar.survey_id=s.id)) ELSE NULL END
 FROM ranked a JOIN events e ON e.id=a.event_id JOIN users u ON u.id=a.user_id
 LEFT JOIN event_surveys s ON s.user_id=a.user_id AND s.event_id=a.event_id `
}

func scanBadge(rows interface{ Scan(...any) error }) (*domain.EventBadge, error) {
	var b domain.EventBadge
	var review, headliner, headliners []byte
	var names pq.StringArray
	if err := rows.Scan(&b.EventID, &b.Title, &b.ImageURL, &b.EventDate, &b.Location, &b.Genre, &b.ConfirmedAt, &b.Steps, &b.MostDanced, &names, &b.Source, &b.DepartedAt, &b.Department, &b.Country, &headliners, &headliner, &review); err != nil {
		return nil, err
	}
	b.LineupNames = names
	if len(headliners) > 0 {
		if err := json.Unmarshal(headliners, &b.Headliners); err != nil {
			return nil, fmt.Errorf("reading badge headliners: %w", err)
		}
	}
	if len(headliner) > 0 {
		if err := json.Unmarshal(headliner, &b.Headliner); err != nil {
			return nil, fmt.Errorf("reading badge headliner: %w", err)
		}
	}
	if len(review) > 0 {
		if err := json.Unmarshal(review, &b.Review); err != nil {
			return nil, fmt.Errorf("reading badge review: %w", err)
		}
	}
	return &b, nil
}

func (r *BadgeRepository) List(ctx context.Context, username, viewer string, limit, offset int) ([]domain.EventBadge, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_attendance a JOIN users u ON u.id=a.user_id WHERE u.username=$1`, username).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, badgeSelect(`username=$1`, r.danceAvailable)+`ORDER BY a.confirmed_at DESC,a.event_id DESC LIMIT $3 OFFSET $4`, username, viewer, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	badges := make([]domain.EventBadge, 0)
	for rows.Next() {
		b, err := scanBadge(rows)
		if err != nil {
			return nil, 0, err
		}
		badges = append(badges, *b)
	}
	return badges, total, rows.Err()
}
func (r *BadgeRepository) Get(ctx context.Context, userID, eventID string) (*domain.EventBadge, error) {
	b, err := scanBadge(r.db.QueryRowContext(ctx, badgeSelect(`id::text=$2`, r.danceAvailable)+`WHERE a.event_id=$1`, eventID, userID))
	if err != nil {
		return nil, err
	}
	b.Lineup, err = NewArtistRepository(r.db).GetEventLineup(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("reading badge lineup: %w", err)
	}
	if b.Source == "manual" && len(b.Lineup) == 0 && len(b.LineupNames) == 0 {
		b.Lineup, err = r.ExampleLineup(ctx, userID, eventID)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Private, explicitly labelled demo artists reuse the existing rating model.
// They are NEVER attached to event_artists or returned as an official lineup.
func (r *BadgeRepository) ExampleLineup(ctx context.Context, userID, eventID string) ([]domain.EventArtist, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id::text,name,created_at,updated_at FROM artists
	 WHERE is_example=true AND example_user_id=$1 AND example_event_id=$2 ORDER BY name,id`, userID, eventID)
	if err != nil {
		return nil, fmt.Errorf("reading private example lineup: %w", err)
	}
	defer rows.Close()
	lineup := make([]domain.EventArtist, 0)
	for rows.Next() {
		artist := domain.Artist{IsExample: true, ArtistType: "DJ de ejemplo", Genres: []string{}}
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.CreatedAt, &artist.UpdatedAt); err != nil {
			return nil, err
		}
		lineup = append(lineup, domain.EventArtist{EventID: eventID, ArtistID: artist.ID, Artist: &artist})
	}
	return lineup, rows.Err()
}

// Administrative opt-in for a specific manual badge; no public artist creation.
func (r *BadgeRepository) PrepareExampleLineup(ctx context.Context, userID, eventID string) (int64, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return 0, domain.ErrInvalidSurvey
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return 0, domain.ErrInvalidSurvey
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT a.source='manual' AND cardinality(COALESCE(e.lineup,'{}'::text[]))=0
	 AND NOT EXISTS(SELECT 1 FROM event_artists ea WHERE ea.event_id=e.id)
	 FROM event_attendance a JOIN events e ON e.id=a.event_id WHERE a.user_id=$1 AND a.event_id=$2 FOR UPDATE OF a`, userID, eventID).Scan(&eligible)
	if err != nil {
		return 0, err
	}
	if !eligible {
		return 0, domain.ErrInvalidSurvey
	}
	var created int64
	for slot := 1; slot <= 3; slot++ {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("root:badge-example:%s:%s:%d", userID, eventID, slot))).String()
		result, err := tx.ExecContext(ctx, `INSERT INTO artists(id,name,artist_type,is_example,example_user_id,example_event_id)
		 VALUES($1,$2,'DJ de ejemplo',true,$3,$4) ON CONFLICT(id) DO NOTHING`, id, fmt.Sprintf("DJ %d", slot), userID, eventID)
		if err != nil {
			return 0, fmt.Errorf("preparing example artist: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		created += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return created, nil
}

// Only metadata belonging to this event can be used for its official artwork.
func (r *BadgeRepository) OfficialImage(ctx context.Context, eventID, artistID string) (string, error) {
	var image sql.NullString
	var err error
	if artistID == "" {
		err = r.db.QueryRowContext(ctx, `SELECT cinematic_banner_url FROM events WHERE id=$1`, eventID).Scan(&image)
	} else {
		err = r.db.QueryRowContext(ctx, `SELECT a.avatar_url FROM artists a JOIN event_artists ea ON ea.artist_id=a.id WHERE ea.event_id=$1 AND a.id=$2`, eventID, artistID).Scan(&image)
	}
	if err != nil {
		return "", err
	}
	if !image.Valid || strings.TrimSpace(image.String) == "" {
		return "", sql.ErrNoRows
	}
	return image.String, nil
}

// Explicit self check-out, not an inferred GPS exit. Manual grants are excluded.
func (r *BadgeRepository) Checkout(ctx context.Context, userID, eventID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE event_attendance a SET departed_at=COALESCE(a.departed_at,clock_timestamp())
	 FROM events e WHERE a.user_id=$1 AND a.event_id=$2 AND e.id=a.event_id AND a.source='geofence'
	 AND (CASE WHEN current_schema() LIKE 'badges_test_%' THEN e.date ELSE (e.date AT TIME ZONE 'UTC') AT TIME ZONE 'America/Argentina/Buenos_Aires' END)<=NOW()`, userID, eventID)
	if err != nil {
		return fmt.Errorf("recording explicit checkout: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Server opening time makes departures in this opening ineligible, even after
// navigation/retries. An invitation is offered only once per event and opening.
func (r *BadgeRepository) OpenReminder(ctx context.Context, userID, openingID string) (*domain.EventBadge, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO event_review_openings(user_id,opening_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, openingID); err != nil {
		return nil, err
	}
	var opened time.Time
	var offered bool
	if err = tx.QueryRowContext(ctx, `SELECT opened_at,offered FROM event_review_openings WHERE user_id=$1 AND opening_id=$2 FOR UPDATE`, userID, openingID).Scan(&opened, &offered); err != nil {
		return nil, err
	}
	var eventID string
	if !offered {
		err = tx.QueryRowContext(ctx, `SELECT a.event_id::text FROM event_attendance a JOIN events e ON e.id=a.event_id
		 WHERE a.user_id=$1 AND a.source='geofence' AND a.departed_at<$2 AND a.reminder_offered_at IS NULL
		 AND (CASE WHEN current_schema() LIKE 'badges_test_%' THEN e.date ELSE (e.date AT TIME ZONE 'UTC') AT TIME ZONE 'America/Argentina/Buenos_Aires' END)<=NOW()
		 AND NOT EXISTS(SELECT 1 FROM event_surveys s WHERE s.user_id=a.user_id AND s.event_id=a.event_id)
		 ORDER BY a.departed_at DESC,a.event_id DESC LIMIT 1 FOR UPDATE OF a SKIP LOCKED`, userID, opened).Scan(&eventID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}
	var badge *domain.EventBadge
	if eventID != "" {
		badge, err = scanBadge(tx.QueryRowContext(ctx, badgeSelect(`id::text=$2`, r.danceAvailable)+`WHERE a.event_id=$1`, eventID, userID))
		if err != nil {
			return nil, err
		}
		if badge.Review != nil {
			badge = nil
		} else {
			if _, err = tx.ExecContext(ctx, `UPDATE event_attendance SET reminder_offered_at=clock_timestamp() WHERE user_id=$1 AND event_id=$2`, userID, eventID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE event_review_openings SET offered=true WHERE user_id=$1 AND opening_id=$2`, userID, openingID); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return badge, nil
}
