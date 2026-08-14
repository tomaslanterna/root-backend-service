package handlers

import (
	"net/http"
)

type CommunityHandler struct{}

func NewCommunityHandler() *CommunityHandler {
	return &CommunityHandler{}
}

func (h *CommunityHandler) GetCommunities(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":             "c1",
				"name":           "Techno Argentina",
				"prOwnerId":      "2",
				"coverImageUrl":  "https://...",
				"membersCount":   1250,
				"description":    "Comunidad oficial...",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *CommunityHandler) GetCommunityByID(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":             "c1",
		"name":           "Techno Argentina",
		"prOwnerId":      "2",
		"coverImageUrl":  "https://...",
		"membersCount":   1250,
		"description":    "Comunidad oficial...",
		"posts":          []interface{}{},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *CommunityHandler) JoinCommunity(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success":      true,
		"membersCount": 1251,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
