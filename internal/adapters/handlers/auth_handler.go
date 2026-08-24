package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"root-backend-service/internal/core/ports"
)

type AuthHandler struct {
	authService ports.AuthService
}

func NewAuthHandler(service ports.AuthService) *AuthHandler {
	return &AuthHandler{authService: service}
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	token, user, err := h.authService.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	response := map[string]interface{}{
		"token": token,
		"user":  user,
	}
	respondWithJSON(w, http.StatusOK, response)
}

type RegisterRequest struct {
	Name       string  `json:"name"`
	Username   string  `json:"username"`
	Email      string  `json:"email"`
	Password   string  `json:"password"`
	Role       string  `json:"role"` // Optional
	Dob        *string `json:"dob"`
	DocumentID *string `json:"documentId"`
	Country    *string `json:"country"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.authService.Register(r.Context(), req.Name, req.Username, req.Email, req.Password, req.Role, req.Dob, req.DocumentID, req.Country)
	if err != nil {
		if err.Error() == "user already exists with this email" {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		log.Printf("ERROR registering user: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusCreated, user)
}

type GoogleLoginRequest struct {
	IDToken string `json:"idToken"`
}

func (h *AuthHandler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	var req GoogleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.IDToken == "" {
		http.Error(w, "idToken is required", http.StatusBadRequest)
		return
	}

	token, user, err := h.authService.GoogleLogin(r.Context(), req.IDToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	response := map[string]interface{}{
		"token": token,
		"user":  user,
	}
	respondWithJSON(w, http.StatusOK, response)
}
