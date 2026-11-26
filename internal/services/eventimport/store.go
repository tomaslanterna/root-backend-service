package eventimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"root-backend-service/internal/core/domain"
)

// Store is the database side of the importer.
type Store interface {
	EnsureSchema(ctx context.Context) error
	Upcoming(ctx context.Context, from, to time.Time) ([]Existing, error)
	Venues(ctx context.Context) (map[string]KnownVenue, error)
	Insert(ctx context.Context, ev NewEvent) (string, error)
}

// SQLStore implements Store on PostgreSQL.
type SQLStore struct{ DB *sql.DB }

const importLockID = 7_302_024_117

// Lock stops two imports from running at the same time. The returned function
// releases it.
func (s *SQLStore) Lock(ctx context.Context) (func(), bool, error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, importLockID).Scan(&got); err != nil || !got {
		conn.Close()
		return nil, false, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, importLockID)
		conn.Close()
	}, true, nil
}

func (s *SQLStore) EnsureSchema(ctx context.Context) error {
	for _, stmt := range []string{
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS source_url TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS city TEXT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS events_source_url_uidx ON events (source_url) WHERE source_url IS NOT NULL`,
	} {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("preparando esquema (%s): %w", stmt, err)
		}
	}
	return nil
}

func (s *SQLStore) Upcoming(ctx context.Context, from, to time.Time) ([]Existing, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id::text, title, location, date, COALESCE(to_jsonb(e)->>'source_url', '')
		FROM events e WHERE date >= $1 AND date <= $2`, from.AddDate(0, 0, -1), to.AddDate(0, 0, 1))
	if err != nil {
		return nil, fmt.Errorf("leyendo eventos existentes: %w", err)
	}
	defer rows.Close()
	var out []Existing
	for rows.Next() {
		var e Existing
		if err := rows.Scan(&e.ID, &e.Title, &e.Location, &e.Date, &e.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Venues returns the coordinates already stored for each venue name.
func (s *SQLStore) Venues(ctx context.Context) (map[string]KnownVenue, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT ON (lower(location)) location, latitude::float8, longitude::float8,
			COALESCE(to_jsonb(e)->>'city', ''), COALESCE(department, '')
		FROM events e WHERE latitude IS NOT NULL AND longitude IS NOT NULL
		ORDER BY lower(location), created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("leyendo venues conocidos: %w", err)
	}
	defer rows.Close()
	out := map[string]KnownVenue{}
	for rows.Next() {
		var name string
		var v KnownVenue
		if err := rows.Scan(&name, &v.Lat, &v.Lng, &v.City, &v.Department); err != nil {
			return nil, err
		}
		if InUruguay(v.Lat, v.Lng) {
			out[normalize(name)] = v
		}
	}
	return out, rows.Err()
}

func (s *SQLStore) Insert(ctx context.Context, ev NewEvent) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var tiers any
	var price any
	if len(ev.Tiers) > 0 {
		raw, err := json.Marshal(ev.Tiers)
		if err != nil {
			return "", err
		}
		tiers = string(raw)
		for _, t := range ev.Tiers {
			// El precio "desde" no cuenta cortesías ni canjes: no son precios de venta (si no, el evento sale "Gratis").
			if domain.IsCourtesyTier(t.Name) || domain.IsExchangeTier(t.Name) {
				continue
			}
			if !t.SoldOut && (price == nil || t.Price < price.(float64)) {
				price = t.Price
			}
		}
	}
	var lat, lng, dept, city any
	if ev.HasPlace {
		lat, lng = ev.Place.Lat, ev.Place.Lng
		dept, city = nullIfEmpty(ev.Place.Department), nullIfEmpty(firstNonEmpty(ev.Place.City, ev.City))
	} else {
		city = nullIfEmpty(ev.City)
	}
	var fetched any
	if ev.TiersFetched {
		fetched = time.Now()
	}

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO events (title, date, end_date, location, department, country, city, latitude, longitude,
			cinematic_banner_url, description, lineup, genre, price, is_featured,
			ticket_tiers, ticket_source, ticket_url, ticket_info_fetched_at, source_url)
		VALUES ($1, $2::timestamp, $3, $4, $5, 'UY', $6, $7, $8, $9, $10, $11, $12, $13, false,
			NULLIF($14, '')::jsonb, $15, $16, $17, $18)
		RETURNING id::text`,
		ev.Title, ev.StartsAt.Format("2006-01-02 15:04:05"), ev.EndsAt, ev.VenueName, dept, city, lat, lng,
		nullIfEmpty(ev.BannerKey), ev.Description, pq.Array(nonNil(ev.Lineup)), nullIfEmpty(ev.Genre), price,
		tiers, nullIfEmpty(ev.TicketSource), nullIfEmpty(ev.TicketURL), fetched, nullIfEmpty(ev.SourceURL),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insertando evento %q: %w", ev.Title, err)
	}
	for _, name := range ev.Lineup {
		artistID, err := artistFor(ctx, tx, name, ev.Genre)
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_artists (event_id, artist_id, is_headliner) VALUES ($1, $2, false)
			ON CONFLICT (event_id, artist_id) DO NOTHING`, id, artistID); err != nil {
			return "", fmt.Errorf("vinculando artista %q: %w", name, err)
		}
	}
	return id, tx.Commit()
}

// artistFor reuses an artist with the same name or creates it.
func artistFor(ctx context.Context, tx *sql.Tx, name, genre string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id::text FROM artists WHERE lower(name) = lower($1) AND COALESCE(is_example, false) = false
		ORDER BY created_at LIMIT 1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("buscando artista %q: %w", name, err)
	}
	id = uuid.NewString()
	genres := []string{}
	if genre != "" {
		genres = []string{genre}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artists (id, name, artist_type, genres) VALUES ($1, $2, 'DJ', $3)`,
		id, name, pq.Array(genres)); err != nil {
		return "", fmt.Errorf("creando artista %q: %w", name, err)
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
