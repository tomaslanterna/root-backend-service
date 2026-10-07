package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

type PostHandler struct {
	postService ports.PostService
	s3Service   s3.S3Service
}

func NewPostHandler(svc ports.PostService, s3Service s3.S3Service) *PostHandler {
	return &PostHandler{
		postService: svc,
		s3Service:   s3Service,
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

	// Transform S3 keys to presigned URLs
	for feedKey, feedData := range response {
		for i := range feedData.Data {
			if feedData.Data[i].HeaderImageURL != nil && *feedData.Data[i].HeaderImageURL != "" && !strings.HasPrefix(*feedData.Data[i].HeaderImageURL, "http") {
				url, err := h.s3Service.GenerateViewUrl(r.Context(), *feedData.Data[i].HeaderImageURL, 7*24*time.Hour)
				if err == nil {
					feedData.Data[i].HeaderImageURL = &url
				}
			}
			if feedData.Data[i].AuthorAvatar != "" && !strings.HasPrefix(feedData.Data[i].AuthorAvatar, "http") {
				url, err := h.s3Service.GenerateViewUrl(r.Context(), feedData.Data[i].AuthorAvatar, 7*24*time.Hour)
				if err == nil {
					feedData.Data[i].AuthorAvatar = url
				}
			}
		}
		response[feedKey] = feedData
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

	if post.HeaderImageURL != nil && *post.HeaderImageURL != "" && !strings.HasPrefix(*post.HeaderImageURL, "http") {
		url, err := h.s3Service.GenerateViewUrl(r.Context(), *post.HeaderImageURL, 7*24*time.Hour)
		if err == nil {
			post.HeaderImageURL = &url
		}
	}
	if post.AuthorAvatar != "" && !strings.HasPrefix(post.AuthorAvatar, "http") {
		url, err := h.s3Service.GenerateViewUrl(r.Context(), post.AuthorAvatar, 7*24*time.Hour)
		if err == nil {
			post.AuthorAvatar = url
		}
	}

	respondWithJSON(w, http.StatusOK, post)
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

func (h *PostHandler) CreatePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title          string `json:"title"`
		Content        string `json:"content"`
		LongContent    string `json:"long_content"`
		HeaderImageURL string `json:"header_image_url"`
		CommunityID    string `json:"community_id"`
		EventID        string `json:"event_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	post := &domain.Post{
		AuthorID:       userID,
		Title:          strPtr(req.Title),
		Content:        req.Content,
		LongContent:    strPtr(req.LongContent),
		HeaderImageURL: strPtr(req.HeaderImageURL),
		CommunityID:    strPtr(req.CommunityID),
		EventID:        strPtr(req.EventID),
	}

	if err := h.postService.CreatePost(r.Context(), post); err != nil {
		if errors.Is(err, domain.ErrCommunityForbidden) {
			respondWithError(w, http.StatusForbidden, "No tenés permiso para publicar en esta comunidad")
		} else if errors.Is(err, domain.ErrCommunityNotFound) {
			respondWithError(w, http.StatusNotFound, "Comunidad no encontrada")
		} else {
			respondWithError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	respondWithJSON(w, http.StatusCreated, post)
}

func (h *PostHandler) GetCommunityAnnouncements(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseCommunityPagination(r, 10)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	posts, total, err := h.postService.GetCommunityAnnouncements(r.Context(), chi.URLParam(r, "id"), limit, offset)
	if err != nil {
		if errors.Is(err, domain.ErrCommunityNotFound) {
			respondWithError(w, http.StatusNotFound, "Comunidad no encontrada")
		} else {
			respondWithError(w, http.StatusInternalServerError, "No se pudieron obtener los anuncios")
		}
		return
	}
	for index := range posts {
		h.signPostMedia(r, &posts[index])
	}
	// Preserve PostgreSQL microsecond precision; the browser must not decide the read watermark.
	readThroughPostID := latestCommunityAnnouncementID(posts)
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"data":              posts,
		"readThroughPostId": readThroughPostID,
		"meta": map[string]interface{}{
			"total": total, "limit": limit, "offset": offset,
			"hasMore": offset+len(posts) < total,
		},
	})
}

func latestCommunityAnnouncementID(posts []domain.Post) string {
	var latest *domain.Post
	for i := range posts {
		post := &posts[i]
		if latest == nil || post.Timestamp.After(latest.Timestamp) || (post.Timestamp.Equal(latest.Timestamp) && post.ID > latest.ID) {
			latest = post
		}
	}
	if latest == nil {
		return ""
	}
	return latest.ID
}

func (h *PostHandler) CreateCommunityAnnouncement(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	var request struct {
		Title          string `json:"title"`
		Content        string `json:"content"`
		LongContent    string `json:"longContent"`
		HeaderImageURL string `json:"headerImageUrl"`
		EventID        string `json:"eventId"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cuerpo de solicitud inválido")
		return
	}
	post := &domain.Post{
		Title:          strPtr(request.Title),
		Content:        request.Content,
		LongContent:    strPtr(request.LongContent),
		HeaderImageURL: strPtr(request.HeaderImageURL),
		EventID:        strPtr(request.EventID),
	}
	created, err := h.postService.CreateCommunityAnnouncement(r.Context(), chi.URLParam(r, "id"), userID, post)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrCommunityNotFound):
			respondWithError(w, http.StatusNotFound, "Comunidad no encontrada")
		case errors.Is(err, domain.ErrCommunityForbidden):
			respondWithError(w, http.StatusForbidden, "No tenés permiso para publicar en esta comunidad")
		case strings.Contains(err.Error(), "cannot exceed"), strings.Contains(err.Error(), "must have"):
			respondWithError(w, http.StatusBadRequest, err.Error())
		default:
			respondWithError(w, http.StatusInternalServerError, "No se pudo crear el anuncio")
		}
		return
	}
	h.signPostMedia(r, created)
	respondWithJSON(w, http.StatusCreated, created)
}

func (h *PostHandler) signPostMedia(r *http.Request, post *domain.Post) {
	if h.s3Service == nil {
		return
	}
	if post.HeaderImageURL != nil && *post.HeaderImageURL != "" && !strings.HasPrefix(*post.HeaderImageURL, "http") {
		if url, err := h.s3Service.GenerateViewUrl(r.Context(), *post.HeaderImageURL, 7*24*time.Hour); err == nil {
			post.HeaderImageURL = &url
		}
	}
	if post.AuthorAvatar != "" && !strings.HasPrefix(post.AuthorAvatar, "http") {
		if url, err := h.s3Service.GenerateViewUrl(r.Context(), post.AuthorAvatar, 7*24*time.Hour); err == nil {
			post.AuthorAvatar = url
		}
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UploadImage handles uploading an image for a post and returns the S3 key
func (h *PostHandler) UploadImage(w http.ResponseWriter, r *http.Request) {
	if h.s3Service == nil {
		respondWithError(w, http.StatusInternalServerError, "S3 Service not initialized")
		return
	}

	err := r.ParseMultipartForm(10 << 20) // 10MB limit
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large or invalid")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "No image file provided")
		return
	}
	defer file.Close()

	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		userID = "anonymous"
	}

	key := fmt.Sprintf("posts/%s/%d_%s", userID, time.Now().Unix(), header.Filename)
	_, s3Err := h.s3Service.UploadToS3(r.Context(), file, key, header.Header.Get("Content-Type"))
	if s3Err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload image to S3")
		return
	}

	// We return the key so the frontend can send it in the CreatePost request
	respondWithJSON(w, http.StatusOK, map[string]string{
		"key": key,
	})
}
