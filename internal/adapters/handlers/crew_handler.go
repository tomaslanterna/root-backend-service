package handlers

import (
	"net/http"
)

type CrewHandler struct{}

func NewCrewHandler() *CrewHandler {
	return &CrewHandler{}
}

func (h *CrewHandler) GetDeck(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":      "sq1",
				"eventId": "e1",
				"name":    "Afterlife Melodic Crew BA",
				"members": []map[string]interface{}{
					{
						"userId":    "1",
						"hasTicket": true,
						"joinedAt":  "2024-02-17T10:00:00Z",
						"role":      "host",
					},
				},
				"matchScore":    96,
				"departureZone": "Palermo / Recoleta",
				"chatRoomId":    "sq_chat_1",
				"status":        "active",
				"createdAt":     "2024-02-17T10:00:00Z",
				"expiresAt":     "2024-03-10T12:00:00Z",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *CrewHandler) Swipe(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success": true,
		"isMatch": true,
		"matchDetails": map[string]interface{}{
			"squadId":    "sq1",
			"chatRoomId": "sq_chat_1",
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *CrewHandler) GetMatches(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":         "sq1",
				"name":       "Afterlife Melodic Crew BA",
				"chatRoomId": "sq_chat_1",
				"status":     "active",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
