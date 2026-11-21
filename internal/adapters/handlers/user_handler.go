package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"root-backend-service/internal/core/domain"
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
	followStatus, err := h.userService.FollowStatus(r.Context(), currentUserID, user)
	if err != nil {
		followStatus = domain.FollowStatusNone
	}
	// Perfil privado: quien no lo sigue ve nombre, foto y cantidades, pero no el contenido.
	canView := !user.IsPrivate || currentUserID == user.ID || followStatus == domain.FollowStatusFollowing

	// Only the owner sees their private fields (email, dob, document, country).
	var body interface{} = user.PublicProfile()
	if currentUserID != "" && currentUserID == user.ID {
		body = user
	}
	response := map[string]interface{}{
		"user":           body,
		"isFollowing":    isFollowing,
		"followStatus":   followStatus,
		"canViewContent": canView,
	}
	respondWithJSON(w, http.StatusOK, response)
}

type profileUserKey struct{}

// ProfileUserID devuelve el ID del perfil que validó RequireProfileAccess.
func ProfileUserID(r *http.Request) string {
	id, _ := r.Context().Value(profileUserKey{}).(string)
	return id
}

// RequireProfileAccess protege el contenido de /users/{username}/...: si el perfil es privado y quien
// mira no lo sigue, responde 403 {"private": true}. Si pasa, deja el ID del perfil en el contexto.
func (h *UserHandler) RequireProfileAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewerID, _ := r.Context().Value(UserIDKey).(string)
		target, canView, err := h.userService.CanViewProfile(r.Context(), viewerID, chi.URLParam(r, "username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			respondWithError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "No se pudo cargar el perfil")
			return
		}
		if !canView {
			respondWithJSON(w, http.StatusForbidden, map[string]interface{}{"message": "Esta cuenta es privada", "private": true})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), profileUserKey{}, target.ID)))
	})
}

// UpdatePrivacy: PUT /users/me/privacy con {"isPrivate": bool}.
func (h *UserHandler) UpdatePrivacy(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		IsPrivate *bool `json:"isPrivate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IsPrivate == nil {
		respondWithError(w, http.StatusBadRequest, "isPrivate is required")
		return
	}
	if err := h.userService.SetPrivacy(r.Context(), userID, *body.IsPrivate); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			respondWithError(w, http.StatusNotFound, "user not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "No se pudo cambiar la privacidad")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"isPrivate": *body.IsPrivate})
}

// ListFollowRequests: GET /users/me/follow-requests.
func (h *UserHandler) ListFollowRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	users, err := h.userService.ListFollowRequests(r.Context(), userID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron cargar las solicitudes")
		return
	}
	for i := range users {
		h.resolveAvatar(r, users[i].AvatarURL)
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": users, "meta": map[string]interface{}{"total": len(users)}})
}

// AcceptFollowRequest: POST /users/me/follow-requests/{username}/accept.
func (h *UserHandler) AcceptFollowRequest(w http.ResponseWriter, r *http.Request) {
	h.answerFollowRequest(w, r, h.userService.AcceptFollowRequest, "accepted")
}

// RejectFollowRequest: DELETE /users/me/follow-requests/{username}.
func (h *UserHandler) RejectFollowRequest(w http.ResponseWriter, r *http.Request) {
	h.answerFollowRequest(w, r, h.userService.RejectFollowRequest, "rejected")
}

func (h *UserHandler) answerFollowRequest(w http.ResponseWriter, r *http.Request, answer func(context.Context, string, string) error, status string) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := answer(r.Context(), userID, chi.URLParam(r, "username"))
	switch {
	case errors.Is(err, domain.ErrUserNotFound), errors.Is(err, domain.ErrFollowRequestNotFound):
		respondWithError(w, http.StatusNotFound, "No hay una solicitud pendiente de esa persona")
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "No se pudo responder la solicitud")
	default:
		respondWithJSON(w, http.StatusOK, map[string]string{"status": status})
	}
}

// ListFollowers: GET /users/{username}/followers?limit&offset (sesión opcional, marca a quién ya seguís).
func (h *UserHandler) ListFollowers(w http.ResponseWriter, r *http.Request) {
	h.listFollows(w, r, h.userService.GetFollowers)
}

// ListFollowing: GET /users/{username}/following?limit&offset.
func (h *UserHandler) ListFollowing(w http.ResponseWriter, r *http.Request) {
	h.listFollows(w, r, h.userService.GetFollowing)
}

type followLister func(ctx context.Context, targetUsername, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error)

func (h *UserHandler) listFollows(w http.ResponseWriter, r *http.Request, list followLister) {
	username := chi.URLParam(r, "username")
	if username == "" {
		respondWithError(w, http.StatusBadRequest, "username is required")
		return
	}
	limit, offset, err := parsePagination(r, defaultRelatedPageSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	viewerID, _ := r.Context().Value(UserIDKey).(string)

	users, total, err := list(r.Context(), username, viewerID, limit, offset)
	if errors.Is(err, domain.ErrUserNotFound) {
		respondWithError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error obteniendo la lista")
		return
	}
	for i := range users {
		h.resolveAvatar(r, users[i].AvatarURL)
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": users,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(users) < total,
		},
	})
}

// GetMyInterests: GET /users/me/interests -> {"genres": [...], "answered": bool, "options": [...]}.
func (h *UserHandler) GetMyInterests(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	genres, answered, err := h.userService.GetInterests(r.Context(), userID)
	if errors.Is(err, domain.ErrUserNotFound) {
		respondWithError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "No se pudieron cargar tus intereses")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"genres": genres, "answered": answered, "options": domain.MusicGenres})
}

// UpdateMyInterests: PUT /users/me/interests con {"genres": [...]}. Una lista vacía es "ahora no".
func (h *UserHandler) UpdateMyInterests(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Genres []string `json:"genres"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	genres, err := h.userService.SetInterests(r.Context(), userID, body.Genres)
	switch {
	case errors.Is(err, domain.ErrInvalidInterests):
		respondWithError(w, http.StatusBadRequest, "Elegí hasta 8 géneros de la lista")
	case errors.Is(err, domain.ErrUserNotFound):
		respondWithError(w, http.StatusNotFound, "user not found")
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "No se pudieron guardar tus intereses")
	default:
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"genres": genres, "answered": true})
	}
}

func (h *UserHandler) FollowUser(w http.ResponseWriter, r *http.Request) {
	targetUsername := chi.URLParam(r, "username")
	currentUserID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || currentUserID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	status, err := h.userService.FollowUser(r.Context(), currentUserID, targetUsername)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// "following" si sigue al instante; "requested" si el perfil es privado y quedó pendiente.
	respondWithJSON(w, http.StatusOK, map[string]string{"status": status})
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
		Name       string `json:"name"`
		Username   string `json:"username"`
		Dob        string `json:"dob"`
		DocumentID string `json:"documentId"`
		Country    string `json:"country"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.userService.UpdateUser(r.Context(), currentUserID, req.Name, req.Username, req.Dob, req.DocumentID, req.Country)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidFullName) {
			respondWithJSON(w, http.StatusBadRequest, map[string]string{"message": "Ingresá tu nombre y apellido."})
			return
		}
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
