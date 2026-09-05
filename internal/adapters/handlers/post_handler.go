package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"root-backend-service/internal/core/ports"
)

type PostHandler struct {
	postService ports.PostService
}

func NewPostHandler(svc ports.PostService) *PostHandler {
	return &PostHandler{
		postService: svc,
	}
}

// Helper para parsear enteros de los query params con un valor default
func getQueryInt(r *http.Request, key string, defaultVal int) int {
	valStr := r.URL.Query().Get(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil || val < 1 {
		return defaultVal
	}
	return val
}

func (h *PostHandler) GetPosts(w http.ResponseWriter, r *http.Request) {
	includeFeedsQuery := r.URL.Query().Get("include_feeds")
	if includeFeedsQuery == "" {
		includeFeedsQuery = "global"
	}

	feedsRaw := strings.Split(includeFeedsQuery, ",")
	var includeFeeds []string
	for _, f := range feedsRaw {
		includeFeeds = append(includeFeeds, strings.TrimSpace(f))
	}

	pagination := make(map[string]int)
	for _, feed := range includeFeeds {
		pagination[feed+"_page"] = getQueryInt(r, feed+"_page", 1)
		pagination[feed+"_limit"] = getQueryInt(r, feed+"_limit", 20)
	}

	var userID string
	if val := r.Context().Value(UserIDKey); val != nil {
		userID = val.(string)
	}

	response, err := h.postService.GetFeeds(r.Context(), userID, includeFeeds, pagination)
	if err != nil {
		http.Error(w, "Failed to retrieve posts", http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusOK, response)
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
