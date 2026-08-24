package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	kycservice "root-backend-service/internal/services/kyc"
	s3service "root-backend-service/internal/services/s3"

	"github.com/go-chi/chi/v5"
)

type KycHandler struct {
	s3Service   s3service.S3Service
	kycProvider kycservice.KycProviderService
	repo        ports.KycRepository
	userRepo    ports.UserRepository
}

func NewKycHandler(s3Service s3service.S3Service, kycProvider kycservice.KycProviderService, repo ports.KycRepository, userRepo ports.UserRepository) *KycHandler {
	return &KycHandler{
		s3Service:   s3Service,
		kycProvider: kycProvider,
		repo:        repo,
		userRepo:    userRepo,
	}
}

type CreateSessionRequest struct {
	UserID string `json:"userId"`
}

// CreateSession initializes a new KYC session
func (h *KycHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		// Fallback to a mock/default if not provided, for backwards compatibility
		req.UserID = "11111111-1111-1111-1111-111111111111" 
	}
	
	sessionID := fmt.Sprintf("kyc_sess_%d", time.Now().UnixNano())
	userID := req.UserID

	// 1. Check if user already has an active or approved session
	lastSession, err := h.repo.GetLastSessionByUserID(r.Context(), userID)
	if err == nil && lastSession != nil {
		// If approved, don't allow new sessions
		if lastSession.Status == "APPROVED" {
			respondWithJSON(w, http.StatusConflict, map[string]string{"error": "User already has an approved KYC session"})
			return
		}

		// If in progress, return the existing one (or you could return an error)
		if lastSession.Status == "CREATED" || lastSession.Status == "DOCUMENT_UPLOADED" || lastSession.Status == "FACE_UPLOADED" || lastSession.Status == "PROCESSING" {
			resp := map[string]interface{}{
				"sessionId": lastSession.ID,
				"status":    lastSession.Status,
				"message":   "Returned existing active session",
				"expiresAt": lastSession.CreatedAt.Add(24 * time.Hour).Format(time.RFC3339),
			}
			respondWithJSON(w, http.StatusOK, resp)
			return
		}
	}

	session := &domain.KycSession{
		ID:              sessionID,
		UserID:          userID,
		Status:          "CREATED",
		DocumentType:    "ID_CARD",
		DocumentCountry: "URY",
	}

	errSession := h.repo.CreateSession(r.Context(), session)
	if errSession != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create session in DB"})
		return
	}

	resp := map[string]interface{}{
		"sessionId": sessionID,
		"status":    "CREATED",
		"expiresAt": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}
	respondWithJSON(w, http.StatusCreated, resp)
}

// UploadDocument handles the upload of the ID card images
func (h *KycHandler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Max file size is 10MB"})
		return
	}

	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	session, err := h.repo.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	file, header, err := r.FormFile("frontImage")
	if err == nil {
		defer file.Close()
		key := fmt.Sprintf("kyc/%s/doc_front_%s", sessionID, header.Filename)
		_, s3Err := h.s3Service.UploadToS3(r.Context(), file, key, header.Header.Get("Content-Type"))
		if s3Err != nil {
			respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to upload front document to S3"})
			return
		}
		session.DocFrontURL = key
	}

	backFile, backHeader, errBack := r.FormFile("backImage")
	if errBack == nil {
		defer backFile.Close()
		key := fmt.Sprintf("kyc/%s/doc_back_%s", sessionID, backHeader.Filename)
		_, s3Err := h.s3Service.UploadToS3(r.Context(), backFile, key, backHeader.Header.Get("Content-Type"))
		if s3Err != nil {
			respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to upload back document to S3"})
			return
		}
		session.DocBackURL = key
	}

	if err != nil && errBack != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Debes adjuntar al menos frontImage o backImage"})
		return
	}

	session.Status = "DOCUMENT_UPLOADED"
	if errUpdate := h.repo.UpdateSession(r.Context(), session); errUpdate != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update session in DB"})
		return
	}

	resp := map[string]interface{}{
		"message": "Document uploaded and saved successfully",
		"status":  session.Status,
	}
	respondWithJSON(w, http.StatusOK, resp)
}

// UploadFace handles the upload of the selfie/liveness video
func (h *KycHandler) UploadFace(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Max file size is 10MB"})
		return
	}

	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	session, err := h.repo.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	file, header, err := r.FormFile("faceMedia")
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "faceMedia file is required"})
		return
	}
	defer file.Close()

	key := fmt.Sprintf("kyc/%s/face_%s", sessionID, header.Filename)
	_, s3Err := h.s3Service.UploadToS3(r.Context(), file, key, header.Header.Get("Content-Type"))
	if s3Err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to upload face media to S3"})
		return
	}

	session.FaceURL = key
	session.Status = "FACE_UPLOADED"
	if errUpdate := h.repo.UpdateSession(r.Context(), session); errUpdate != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update session in DB"})
		return
	}

	resp := map[string]interface{}{
		"message": "Face media uploaded successfully",
		"status":  session.Status,
	}
	respondWithJSON(w, http.StatusOK, resp)
}

// SubmitSession triggers the evaluation
func (h *KycHandler) SubmitSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return
	}

	session, err := h.repo.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	session.Status = "PROCESSING"
	h.repo.UpdateSession(r.Context(), session)

	// Fetch expected user data
	expectedName := ""
	expectedDocID := ""
	expectedCountry := ""
	if h.userRepo != nil {
		user, err := h.userRepo.GetUserByID(r.Context(), session.UserID)
		if err == nil && user != nil {
			expectedName = user.Name
			if user.DocumentID != nil {
				expectedDocID = *user.DocumentID
			}
			if user.Country != nil {
				expectedCountry = *user.Country
			}
		}
	}

	result, err := h.kycProvider.AnalyzeIdentity(r.Context(), sessionID, session.DocFrontURL, session.DocBackURL, session.FaceURL, expectedName, expectedDocID, expectedCountry)
	if err != nil {
		session.Status = "REJECTED"
		session.FailureReason = err.Error()
		h.repo.UpdateSession(r.Context(), session)

		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Error analizando identidad", "reason": err.Error()})
		return
	}

	if result.MatchScore >= 90 {
		session.Status = "APPROVED"
		// Update user profile!
		if h.userRepo != nil {
			h.userRepo.UpdateUserKycStatus(r.Context(), session.UserID, true, result.ExtractedData.Country)
		}
	} else {
		session.Status = "FAILED"
	}
	session.MatchScore = result.MatchScore

	extractedJSON, _ := json.Marshal(result.ExtractedData)
	session.ExtractedData = string(extractedJSON)
	h.repo.UpdateSession(r.Context(), session)

	resp := map[string]interface{}{
		"message": "Verification completed",
		"status":  session.Status,
		"score":   session.MatchScore,
	}
	respondWithJSON(w, http.StatusAccepted, resp)
}

// GetStatus returns the current status of the KYC session
func (h *KycHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")

	session, err := h.repo.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	var extractedData map[string]interface{}
	if session.ExtractedData != "" {
		json.Unmarshal([]byte(session.ExtractedData), &extractedData)
	}

	resp := map[string]interface{}{
		"sessionId":     session.ID,
		"status":        session.Status,
		"matchScore":    session.MatchScore,
		"extractedData": extractedData,
		"failureReason": session.FailureReason,
	}
	respondWithJSON(w, http.StatusOK, resp)
}

func (h *KycHandler) WebhookProvider(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"message": "Webhook received",
	}
	respondWithJSON(w, http.StatusOK, resp)
}
