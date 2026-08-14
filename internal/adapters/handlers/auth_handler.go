package handlers

import (
	"net/http"
)

type AuthHandler struct{}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"token": "jwt-token-string",
		"user": map[string]interface{}{
			"id":            "1",
			"name":          "Admin Root",
			"username":      "admin",
			"role":          "ADMIN",
			"avatarUrl":     "https://...",
			"isKycVerified": true,
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
