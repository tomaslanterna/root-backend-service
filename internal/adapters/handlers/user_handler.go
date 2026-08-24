package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type UserHandler struct {
	userService ports.UserService
}

func NewUserHandler(userService ports.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
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

	response := map[string]interface{}{
		"user":        user,
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
