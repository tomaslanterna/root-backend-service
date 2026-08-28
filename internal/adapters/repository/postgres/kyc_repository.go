package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"
)

type kycRepository struct {
	db *sql.DB
}

func NewKycRepository(db *sql.DB) ports.KycRepository {
	return &kycRepository{
		db: db,
	}
}



func (r *kycRepository) CreateSession(ctx context.Context, session *domain.KycSession) error {
	query := `
		INSERT INTO kyc_sessions (id, user_id, status, document_type, document_country, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.ExecContext(ctx, query,
		session.ID,
		session.UserID,
		session.Status,
		session.DocumentType,
		session.DocumentCountry,
		time.Now(),
		time.Now(),
	)
	return err
}

func (r *kycRepository) GetSessionByID(ctx context.Context, id string) (*domain.KycSession, error) {
	query := `
		SELECT id, user_id, status, document_type, document_country, 
		       COALESCE(doc_front_url, ''), COALESCE(doc_back_url, ''), COALESCE(face_url, ''), 
		       COALESCE(match_score, 0), COALESCE(extracted_data::text, ''), COALESCE(failure_reason, ''), 
		       created_at, updated_at
		FROM kyc_sessions
		WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, query, id)

	var session domain.KycSession
	err := row.Scan(
		&session.ID, &session.UserID, &session.Status, &session.DocumentType, &session.DocumentCountry,
		&session.DocFrontURL, &session.DocBackURL, &session.FaceURL,
		&session.MatchScore, &session.ExtractedData, &session.FailureReason,
		&session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		return nil, err
	}

	return &session, nil
}

func (r *kycRepository) UpdateSession(ctx context.Context, session *domain.KycSession) error {
	query := `
		UPDATE kyc_sessions
		SET status = $1, doc_front_url = $2, doc_back_url = $3, face_url = $4,
		    match_score = $5, extracted_data = NULLIF($6, '')::jsonb, failure_reason = $7, updated_at = $8
		WHERE id = $9
	`
	_, err := r.db.ExecContext(ctx, query,
		session.Status,
		session.DocFrontURL,
		session.DocBackURL,
		session.FaceURL,
		session.MatchScore,
		session.ExtractedData,
		session.FailureReason,
		time.Now(),
		session.ID,
	)
	return err
}

func (r *kycRepository) GetLastSessionByUserID(ctx context.Context, userID string) (*domain.KycSession, error) {
	query := `
		SELECT id, user_id, status, document_type, document_country, 
		       COALESCE(doc_front_url, ''), COALESCE(doc_back_url, ''), COALESCE(face_url, ''), 
		       COALESCE(match_score, 0), COALESCE(extracted_data::text, ''), COALESCE(failure_reason, ''), 
		       created_at, updated_at
		FROM kyc_sessions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, userID)

	var session domain.KycSession
	err := row.Scan(
		&session.ID, &session.UserID, &session.Status, &session.DocumentType, &session.DocumentCountry,
		&session.DocFrontURL, &session.DocBackURL, &session.FaceURL,
		&session.MatchScore, &session.ExtractedData, &session.FailureReason,
		&session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		return nil, err
	}

	return &session, nil
}
