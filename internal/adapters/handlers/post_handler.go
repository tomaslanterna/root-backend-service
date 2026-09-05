package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
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

func (h *PostHandler) GetPostByID(w http.ResponseWriter, r *http.Request) {
	postID := chi.URLParam(r, "id")
	if postID == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}
	
	var userID string
	if val := r.Context().Value(UserIDKey); val != nil {
		userID = val.(string)
	}

	post, err := h.postService.GetPostByID(r.Context(), postID, userID)
	if err != nil {
		if err.Error() == "post not found" {
			http.Error(w, "Post not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to retrieve post", http.StatusInternalServerError)
		}
		return
	}

	respondWithJSON(w, http.StatusOK, post)
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

func (h *PostHandler) GetPostComments(w http.ResponseWriter, r *http.Request) {
	limit := getQueryInt(r, "limit", 20)
	offset := getQueryInt(r, "offset", 0)

	comments, total, err := h.postService.GetPostComments(r.Context(), chi.URLParam(r, "id"), limit, offset)
	if err != nil {
		if err.Error() == "post not found" {
			respondWithError(w, http.StatusNotFound, "Post no encontrado")
		} else {
			respondWithError(w, http.StatusInternalServerError, "Error obteniendo comentarios")
		}
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data": comments,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(comments) < total,
		},
	})
}

func (h *PostHandler) CommentPost(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}

	var request struct {
		Content string `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cuerpo de solicitud inválido")
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	if request.Content == "" {
		respondWithError(w, http.StatusBadRequest, "Contenido de comentario requerido")
		return
	}
	if len(request.Content) > 1000 {
		respondWithError(w, http.StatusBadRequest, "El comentario no puede superar 1000 caracteres")
		return
	}

	comment, err := h.postService.CreatePostComment(r.Context(), chi.URLParam(r, "id"), userID, request.Content)
	if err != nil {
		if err.Error() == "sql: no rows in result set" || err.Error() == "post not found" {
			respondWithError(w, http.StatusNotFound, "Post no encontrado")
		} else {
			respondWithError(w, http.StatusInternalServerError, "Error creando comentario")
		}
		return
	}
	respondWithJSON(w, http.StatusCreated, comment)
}
