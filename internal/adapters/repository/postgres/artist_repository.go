package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
)

type ArtistRepository struct {
	db *sql.DB
}

func NewArtistRepository(db *sql.DB) *ArtistRepository {
	return &ArtistRepository{db: db}
}

func (r *ArtistRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning artist schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS artists (
			id UUID PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			artist_type VARCHAR(100) NOT NULL,
			genres TEXT[] DEFAULT '{}',
			avatar_url TEXT,
			social_links JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artists_name ON artists(name)`,
		
		`CREATE TABLE IF NOT EXISTS event_artists (
			event_id UUID NOT NULL,
			artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
			performance_time TIMESTAMP WITH TIME ZONE,
			is_headliner BOOLEAN DEFAULT FALSE,
			PRIMARY KEY (event_id, artist_id)
		)`,
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing statement %q: %w", stmt, err)
		}
	}

	return tx.Commit()
}

func (r *ArtistRepository) GetEventLineup(ctx context.Context, eventID string) ([]domain.EventArtist, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT 
			ea.event_id, ea.artist_id, ea.performance_time, ea.is_headliner,
			a.id, a.name, a.artist_type, a.genres, COALESCE(a.avatar_url, ''), COALESCE(a.social_links, '{}'::jsonb), a.created_at, a.updated_at
		FROM event_artists ea
		JOIN artists a ON ea.artist_id = a.id
		WHERE ea.event_id::text = $1
		ORDER BY ea.performance_time ASC NULLS LAST
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("querying event lineup: %w", err)
	}
	defer rows.Close()

	var lineup []domain.EventArtist
	for rows.Next() {
		var ea domain.EventArtist
		var a domain.Artist
		var perfTime sql.NullTime
		var avatarUrl sql.NullString
		var socialLinks []byte
		var genres pq.StringArray

		if err := rows.Scan(
			&ea.EventID, &ea.ArtistID, &perfTime, &ea.IsHeadliner,
			&a.ID, &a.Name, &a.ArtistType, &genres, &avatarUrl, &socialLinks, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}

		if perfTime.Valid {
			ea.PerformanceTime = &perfTime.Time
		}
		if avatarUrl.Valid && avatarUrl.String != "" {
			a.AvatarURL = &avatarUrl.String
		}
		a.Genres = genres
		a.SocialLinks = socialLinks

		ea.Artist = &a
		lineup = append(lineup, ea)
	}
	return lineup, nil
}
