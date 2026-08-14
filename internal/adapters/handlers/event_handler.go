package handlers

import (
	"net/http"
)

type EventHandler struct{}

func NewEventHandler() *EventHandler {
	return &EventHandler{}
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":                 "e1",
				"title":              "AFTERLIFE BUENOS AIRES",
				"producerId":         "p1",
				"date":               "2024-03-08",
				"location":           "Mandarine Park",
				"cinematicBannerUrl": "https://...",
				"description":        "Una odisea visual...",
				"lineup":             []string{"Tale Of Us", "Anyma"},
				"goingCount":         184,
				"notGoingCount":      46,
			},
		},
		"meta": map[string]interface{}{
			"total": 15,
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) GetEventByID(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":                 "e1",
		"title":              "AFTERLIFE BUENOS AIRES",
		"producerId":         "p1",
		"date":               "2024-03-08",
		"location":           "Mandarine Park",
		"cinematicBannerUrl": "https://...",
		"description":        "Una odisea visual...",
		"lineup":             []string{"Tale Of Us", "Anyma"},
		"goingCount":         184,
		"notGoingCount":      46,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) RSVPEvent(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success":       true,
		"goingCount":    185,
		"notGoingCount": 46,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) GetEventTickets(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":       "t1",
				"eventId":  "e1",
				"sellerId": "2",
				"price":    45000,
				"status":   "AVAILABLE",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}
