package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"root-backend-service/internal/core/ports"
	s3service "root-backend-service/internal/services/s3"

	"github.com/go-chi/chi/v5"
)

// maxAvatarBytes limita el tamaño de la foto de perfil (el front la comprime a ~512 px, pesa mucho menos).
const maxAvatarBytes = 5 << 20

type UserHandler struct {
	userService ports.UserService
	storage     s3service.S3Service
}

func NewUserHandler(userService ports.UserService, storage s3service.S3Service) *UserHandler {
	return &UserHandler{
		userService: userService,
		storage:     storage,
	}
}

// resolveAvatar convierte una clave de S3 guardada en avatar_url en una URL que el cliente puede cargar.
func (h *UserHandler) resolveAvatar(r *http.Request, avatar *string) {
	if h.storage == nil || avatar == nil || *avatar == "" || strings.HasPrefix(*avatar, "http") {
		return
	}
	if url, err := h.storage.GenerateViewUrl(r.Context(), *avatar, 7*24*time.Hour); err == nil {
		*avatar = url
	}
}

// UpdateAvatar guarda la foto de perfil del usuario autenticado: PUT/POST multipart con el campo "image".
// Sube la imagen a S3 (avatars/<userID>/...), guarda la referencia en users.avatar_url y devuelve el usuario.
func (h *UserHandler) UpdateAvatar(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.storage == nil {
		respondWithError(w, http.StatusServiceUnavailable, "El almacenamiento de imágenes no está disponible")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAvatarBytes); err != nil {
		respondWithError(w, http.StatusRequestEntityTooLarge, "La imagen es demasiado grande (máximo 5 MB)")
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Falta la imagen (campo \"image\")")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
	if err != nil || len(data) == 0 {
		respondWithError(w, http.StatusBadRequest, "No se pudo leer la imagen")
		return
	}
	if len(data) > maxAvatarBytes {
		respondWithError(w, http.StatusRequestEntityTooLarge, "La imagen es demasiado grande (máximo 5 MB)")
		return
	}

	// El tipo se decide por el contenido, no por lo que declare el cliente.
	contentType := http.DetectContentType(data)
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[contentType]
	if ext == "" {
		respondWithError(w, http.StatusUnsupportedMediaType, "Formato no soportado: usá JPG, PNG o WebP")
		return
	}

	key := fmt.Sprintf("avatars/%s/%d.%s", userID, time.Now().Unix(), ext)
	if _, err := h.storage.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType); err != nil {
		log.Printf("UpdateAvatar: upload failed for user %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo subir la imagen")
		return
	}

	viewURL, err := h.storage.GenerateViewUrl(r.Context(), key, 7*24*time.Hour)
	if err != nil {
		log.Printf("UpdateAvatar: view url failed for %s: %v", key, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo preparar la imagen")
		return
	}
	// Con CDN la URL es permanente y se guarda tal cual (todos los endpoints que devuelven avatares la
	// muestran sin más). Sin CDN la URL firmada vence a los 7 días: se guarda la clave y se resuelve al leer.
	stored := key
	if !strings.Contains(viewURL, "X-Amz-") {
		stored = viewURL
	}

	user, err := h.userService.UpdateAvatar(r.Context(), userID, stored)
	if err != nil {
		log.Printf("UpdateAvatar: save failed for user %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo guardar la foto de perfil")
		return
	}
	user.AvatarURL = &viewURL
	respondWithJSON(w, http.StatusOK, user)
}

func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	currentUserID := ""
	if id, ok := r.Context().Value(UserIDKey).(string); ok {
		currentUserID = id
	}

	user, isFollowing, err := h.userService.GetUserByUsername(r.Context(), username, currentUserID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	h.resolveAvatar(r, user.AvatarURL)
	// Only the owner sees their private fields (email, dob, document, country).
	var body interface{} = user.PublicProfile()
	if currentUserID != "" && currentUserID == user.ID {
		body = user
	}
	response := map[string]interface{}{
		"user":        body,
		"isFollowing": isFollowing,
	}
	respondWithJSON(w, http.StatusOK, response)
}

func (h *UserHandler) FollowUser(w http.ResponseWriter, r *http.Request) {
	targetUsername := chi.URLParam(r, "username")
	currentUserID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || currentUserID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.userService.FollowUser(r.Context(), currentUserID, targetUsername); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "followed"})
}

func (h *UserHandler) UnfollowUser(w http.ResponseWriter, r *http.Request) {
	targetUsername := chi.URLParam(r, "username")
	currentUserID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || currentUserID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.userService.UnfollowUser(r.Context(), currentUserID, targetUsername); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "unfollowed"})
}

func (h *UserHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	// ... keeping mocked for now or could implement it, but keeping as is
	mockResponse := map[string]interface{}{
		"id":            "1",
		"name":          "Admin Root",
		"username":      "admin",
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	currentUserID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || currentUserID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Username   string `json:"username"`
		Dob        string `json:"dob"`
		DocumentID string `json:"documentId"`
		Country    string `json:"country"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.userService.UpdateUser(r.Context(), currentUserID, req.Username, req.Dob, req.DocumentID, req.Country)
	if err != nil {
		if err.Error() == "username is already taken" {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusOK, user)
}

func (h *UserHandler) CheckUsername(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username == "" {
		http.Error(w, "username query parameter is required", http.StatusBadRequest)
		return
	}
	username = strings.ToLower(username)

	_, _, err := h.userService.GetUserByUsername(r.Context(), username, "")
	if err != nil {
		// If error is not found (which means it's available)
		if err.Error() == "user not found" || err.Error() == "sql: no rows in result set" {
			respondWithJSON(w, http.StatusOK, map[string]bool{"available": true})
			return
		}
		// Some other DB error
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Found
	respondWithJSON(w, http.StatusOK, map[string]bool{"available": false})
}
