package handlers

import (
	"net/http"
)

type PostHandler struct{}

func NewPostHandler() *PostHandler {
	return &PostHandler{}
}

func (h *PostHandler) GetPosts(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":             "p1",
				"authorId":       "2",
				"eventId":        "e1",
				"communityId":    nil,
				"title":          "Lanzamiento de tickets",
				"content":        "¡Ya están disponibles...",
				"longContent":    "La preventa oficial...",
				"headerImageUrl": "https://...",
				"timestamp":      "2024-02-15T10:00:00Z",
				"likesCount":     145,
			},
		},
		"meta": map[string]interface{}{
			"nextPage": 2,
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *PostHandler) CreatePost(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":        "p2",
		"authorId":  "1",
		"timestamp": "2024-02-15T12:00:00Z",
	}
	respondWithJSON(w, http.StatusCreated, mockResponse)
}

func (h *PostHandler) LikePost(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success":    true,
		"likesCount": 146,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *PostHandler) CommentPost(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":        "cm1",
		"targetId":  "p1",
		"authorId":  "1",
		"content":   "Excelente data",
		"timestamp": "2024-02-15T12:05:00Z",
	}
	respondWithJSON(w, http.StatusCreated, mockResponse)
}
