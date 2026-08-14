package handlers

import (
	"net/http"
)

type UserHandler struct{}

func NewUserHandler() *UserHandler {
	return &UserHandler{}
}

func (h *UserHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":            "1",
		"name":          "Admin Root",
		"username":      "admin",
		"role":          "ADMIN",
		"avatarUrl":     "https://...",
		"isKycVerified": true,
		"vibeProfile": map[string]interface{}{
			"favoriteGenres":   []string{"Melodic Techno", "Progressive House"},
			"departureZone":    "Palermo / Recoleta",
			"partyStyle":       "full_night",
			"verifiedKycOnly":  true,
			"spotifyConnected": true,
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success": true,
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

func (h *UserHandler) GetUserByID(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":            "2",
		"name":          "Alex RRPP",
		"username":      "alex_rrpp",
		"avatarUrl":     "https://...",
		"isKycVerified": true,
		"publicVibeProfile": map[string]interface{}{
			"favoriteGenres": []string{"Hard Techno"},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
