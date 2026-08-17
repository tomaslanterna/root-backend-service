package domain

import (
	"time"
)

type KycSession struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	Status          string    `json:"status"` // CREATED, DOCUMENT_UPLOADED, FACE_UPLOADED, PROCESSING, APPROVED, REJECTED, MANUAL_REVIEW
	DocumentType    string    `json:"document_type"`
	DocumentCountry string    `json:"document_country"`
	DocFrontURL     string    `json:"doc_front_url"`
	DocBackURL      string    `json:"doc_back_url"`
	FaceURL         string    `json:"face_url"`
	MatchScore      float64   `json:"match_score"`
	ExtractedData   string    `json:"extracted_data"`
	FailureReason   string    `json:"failure_reason"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
