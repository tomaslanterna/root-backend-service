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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning survey schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
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

	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM event_surveys WHERE user_id::text = $1 AND event_id::text = $2)", survey.UserID, survey.EventID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking existing survey: %w", err)
	}
	if exists {
		return fmt.Errorf("user already submitted a survey for this event")
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO event_surveys (id, user_id, event_id, general_rating, organization_rating, vibe_rating, sound_visual_rating, pricing_rating, space_rating, would_return, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, survey.ID, survey.UserID, survey.EventID, survey.GeneralRating, survey.OrganizationRating, survey.VibeRating, survey.SoundVisualRating, survey.PricingRating, survey.SpaceRating, survey.WouldReturn, survey.Comment, survey.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting survey: %w", err)
	}

	for _, ar := range artistRatings {
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
