package handlers

import (
	"bytes"
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
	s3Service     s3service.S3Service
	kycProvider   kycservice.KycProviderService
	repo          ports.KycRepository
	userRepo      ports.UserRepository
	notifications ports.NotificationPublisher
}

func NewKycHandler(s3Service s3service.S3Service, kycProvider kycservice.KycProviderService, repo ports.KycRepository, userRepo ports.UserRepository, publishers ...ports.NotificationPublisher) *KycHandler {
	handler := &KycHandler{
		s3Service:   s3Service,
		kycProvider: kycProvider,
		repo:        repo,
		userRepo:    userRepo,
	}
	if len(publishers) > 0 {
		handler.notifications = publishers[0]
	}
	return handler
}

// kycUserID devuelve el usuario autenticado (lo pone AuthMiddleware). Responde 401 si no hay.
func kycUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return "", false
	}
	return userID, true
}

// ownedSession carga la sesión KYC de la URL solo si pertenece al usuario autenticado. Una sesión ajena
// responde 404 igual que una inexistente, para no revelar qué IDs existen.
func (h *KycHandler) ownedSession(w http.ResponseWriter, r *http.Request) (*domain.KycSession, bool) {
	userID, ok := kycUserID(w, r)
	if !ok {
		return nil, false
	}
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "session id is required"})
		return nil, false
	}
	session, err := h.repo.GetSessionByID(r.Context(), sessionID)
	if err != nil || session == nil || session.UserID != userID {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return nil, false
	}
	return session, true
}

// CreateSession initializes a new KYC session for the authenticated user. Any userId in the body is ignored.
func (h *KycHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := kycUserID(w, r)
	if !ok {
		return
	}

	sessionID := fmt.Sprintf("kyc_sess_%d", time.Now().UnixNano())

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

// maxKycImageBytes: las fotos del flujo KYC llegan como JPEG desde el canvas. Se guardan solo si el
// contenido es realmente una imagen, con una key fija (sin el nombre que manda el cliente).
const maxKycImageBytes = 10 << 20

// UploadDocument handles the upload of the ID card images
func (h *KycHandler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	session, ok := h.ownedSession(w, r)
	if !ok {
		return
	}
	sessionID := session.ID

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Max file size is 10MB"})
		return
	}

	file, _, err := r.FormFile("frontImage")
	if err == nil {
		defer file.Close()
		data, contentType, ext, readErr := readImageUpload(file, maxKycImageBytes, false)
		if readErr != nil {
			status, msg := imageUploadErrorStatus(readErr)
			respondWithJSON(w, status, map[string]string{"error": msg})
			return
		}
		key := fmt.Sprintf("kyc/%s/doc_front.%s", sessionID, ext)
		_, s3Err := h.s3Service.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType)
		if s3Err != nil {
			respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to upload front document to S3"})
			return
		}
		session.DocFrontURL = key
	}

	backFile, _, errBack := r.FormFile("backImage")
	if errBack == nil {
		defer backFile.Close()
		data, contentType, ext, readErr := readImageUpload(backFile, maxKycImageBytes, false)
		if readErr != nil {
			status, msg := imageUploadErrorStatus(readErr)
			respondWithJSON(w, status, map[string]string{"error": msg})
			return
		}
		key := fmt.Sprintf("kyc/%s/doc_back.%s", sessionID, ext)
		_, s3Err := h.s3Service.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType)
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
	session, ok := h.ownedSession(w, r)
	if !ok {
		return
	}
	sessionID := session.ID

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Max file size is 10MB"})
		return
	}

	file, _, err := r.FormFile("faceMedia")
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "faceMedia file is required"})
		return
	}
	defer file.Close()
	data, contentType, ext, readErr := readImageUpload(file, maxKycImageBytes, false)
	if readErr != nil {
		status, msg := imageUploadErrorStatus(readErr)
		respondWithJSON(w, status, map[string]string{"error": msg})
		return
	}

	key := fmt.Sprintf("kyc/%s/face.%s", sessionID, ext)
	_, s3Err := h.s3Service.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType)
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
	session, ok := h.ownedSession(w, r)
	if !ok {
		return
	}
	sessionID := session.ID

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
	if h.notifications != nil {
		approved := session.Status == "APPROVED"
		title, body := "Verificación pendiente", "No pudimos verificar tu identidad. Revisá los datos e intentá nuevamente."
		if approved {
			title, body = "Identidad verificada", "Tu perfil ya cuenta con la verificación de identidad."
		}
		_ = h.notifications.Publish(r.Context(), domain.NotificationInput{UserID: session.UserID, Type: "kyc.status", Category: domain.NotificationAccount,
			Title: title, Body: body, Data: map[string]string{"status": session.Status}, DedupeKey: "kyc.status:" + session.ID + ":" + session.Status})
	}

	resp := map[string]interface{}{
		"message": "Verification completed",
		"status":  session.Status,
		"score":   session.MatchScore,
	}
	respondWithJSON(w, http.StatusAccepted, resp)
}

// GetStatus returns the current status of the KYC session
func (h *KycHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := h.ownedSession(w, r)
	if !ok {
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
