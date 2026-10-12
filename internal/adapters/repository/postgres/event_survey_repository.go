package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"root-backend-service/internal/core/domain"
)

type EventSurveyRepository struct {
	db *sql.DB
}

func NewEventSurveyRepository(db *sql.DB) *EventSurveyRepository {
	return &EventSurveyRepository{db: db}
}

func (r *EventSurveyRepository) InitSchema(ctx context.Context) error {
	return r.initSchema(ctx, true)
}

// During a staged live rollout the older API has no attendance ledger yet.
// Keep its existing review writes working; the updated repository still checks
// attendance on every write. Normal deployments retain the composite FK.
func (r *EventSurveyRepository) InitSchemaCompatible(ctx context.Context) error {
	return r.initSchema(ctx, false)
}

func (r *EventSurveyRepository) initSchema(ctx context.Context, enforceAttendanceFK bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning survey schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_surveys_schema',0))`,
		`CREATE TABLE IF NOT EXISTS event_surveys (
			id UUID PRIMARY KEY,
			user_id UUID NOT NULL,
			event_id UUID NOT NULL,
			general_rating INT NOT NULL CHECK (general_rating >= 1 AND general_rating <= 5),
			organization_rating INT CHECK (organization_rating IS NULL OR (organization_rating >= 1 AND organization_rating <= 5)),
			vibe_rating INT CHECK (vibe_rating IS NULL OR (vibe_rating >= 1 AND vibe_rating <= 5)),
			comment TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, event_id)
		)`,
		`ALTER TABLE event_surveys ADD COLUMN IF NOT EXISTS sound_visual_rating INT CHECK (sound_visual_rating IS NULL OR (sound_visual_rating >= 1 AND sound_visual_rating <= 5))`,
		`ALTER TABLE event_surveys ADD COLUMN IF NOT EXISTS pricing_rating INT CHECK (pricing_rating IS NULL OR (pricing_rating >= 1 AND pricing_rating <= 5))`,
		`ALTER TABLE event_surveys ADD COLUMN IF NOT EXISTS space_rating VARCHAR(50)`,
		`ALTER TABLE event_surveys ADD COLUMN IF NOT EXISTS would_return BOOLEAN`,
		`CREATE TABLE IF NOT EXISTS survey_artist_ratings (
			id UUID PRIMARY KEY,
			survey_id UUID NOT NULL REFERENCES event_surveys(id) ON DELETE CASCADE,
			artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
			rating INT NOT NULL CHECK (rating >= 1 AND rating <= 5),
			UNIQUE(survey_id, artist_id)
		)`,
		`ALTER TABLE event_surveys ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`ALTER TABLE event_surveys DROP CONSTRAINT IF EXISTS event_surveys_general_rating_check,
		 DROP CONSTRAINT IF EXISTS event_surveys_organization_rating_check,
		 DROP CONSTRAINT IF EXISTS event_surveys_vibe_rating_check,
		 DROP CONSTRAINT IF EXISTS event_surveys_sound_visual_rating_check,
		 DROP CONSTRAINT IF EXISTS event_surveys_pricing_rating_check`,
		`ALTER TABLE event_surveys ADD CONSTRAINT event_surveys_general_rating_check CHECK(general_rating BETWEEN 0 AND 5),
		 ADD CONSTRAINT event_surveys_organization_rating_check CHECK(organization_rating BETWEEN 0 AND 5),
		 ADD CONSTRAINT event_surveys_vibe_rating_check CHECK(vibe_rating BETWEEN 0 AND 5),
		 ADD CONSTRAINT event_surveys_sound_visual_rating_check CHECK(sound_visual_rating BETWEEN 0 AND 5),
		 ADD CONSTRAINT event_surveys_pricing_rating_check CHECK(pricing_rating BETWEEN 0 AND 5)`,
		`ALTER TABLE survey_artist_ratings DROP CONSTRAINT IF EXISTS survey_artist_ratings_rating_check`,
		`ALTER TABLE survey_artist_ratings ADD CONSTRAINT survey_artist_ratings_rating_check CHECK(rating BETWEEN 0 AND 5)`,
	}
	if enforceAttendanceFK {
		statements = append(statements, `DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='event_surveys'::regclass AND conname='event_surveys_attendance_fk') THEN
		 ALTER TABLE event_surveys ADD CONSTRAINT event_surveys_attendance_fk FOREIGN KEY(user_id,event_id) REFERENCES event_attendance(user_id,event_id) NOT VALID;
		 END IF; END $$`)
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing statement %q: %w", stmt, err)
		}
	}

	return tx.Commit()
}

func (r *EventSurveyRepository) CreateSurvey(ctx context.Context, survey *domain.EventSurvey, artistRatings []domain.SurveyArtistRating) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning survey transaction: %w", err)
	}
	defer tx.Rollback()

	var confirmed bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM event_attendance WHERE user_id=$1 AND event_id=$2 FOR UPDATE`, survey.UserID, survey.EventID).Scan(&confirmed)
	if err == sql.ErrNoRows {
		return domain.ErrAttendanceRequired
	}
	if err != nil {
		return fmt.Errorf("checking attendance: %w", err)
	}
	if !confirmed {
		return domain.ErrAttendanceRequired
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO event_surveys (id, user_id, event_id, general_rating, organization_rating, vibe_rating, sound_visual_rating, pricing_rating, space_rating, would_return, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT(user_id,event_id) DO NOTHING
		RETURNING id,created_at,updated_at
	`, survey.ID, survey.UserID, survey.EventID, survey.GeneralRating, survey.OrganizationRating, survey.VibeRating, survey.SoundVisualRating, survey.PricingRating, survey.SpaceRating, survey.WouldReturn, survey.Comment, survey.CreatedAt).Scan(&survey.ID, &survey.CreatedAt, &survey.UpdatedAt)
	if err == sql.ErrNoRows {
		return domain.ErrSurveyConfirmed
	}
	if err != nil {
		return fmt.Errorf("inserting survey: %w", err)
	}

	for _, ar := range artistRatings {
		var belongs bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_artists WHERE event_id=$1 AND artist_id=$2)
		 OR EXISTS(SELECT 1 FROM artists art JOIN event_attendance a ON a.user_id=art.example_user_id AND a.event_id=art.example_event_id
		 JOIN events e ON e.id=a.event_id WHERE art.id=$2 AND art.is_example=true AND art.example_user_id=$3 AND art.example_event_id=$1
		 AND a.source='manual' AND cardinality(COALESCE(e.lineup,'{}'::text[]))=0
		 AND NOT EXISTS(SELECT 1 FROM event_artists ea WHERE ea.event_id=$1))`, survey.EventID, ar.ArtistID, survey.UserID).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return domain.ErrInvalidSurvey
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO survey_artist_ratings (id, survey_id, artist_id, rating)
			VALUES ($1, $2, $3, $4)
		`, ar.ID, survey.ID, ar.ArtistID, ar.Rating)
		if err != nil {
			return fmt.Errorf("inserting artist rating: %w", err)
		}
	}

	return tx.Commit()
}
