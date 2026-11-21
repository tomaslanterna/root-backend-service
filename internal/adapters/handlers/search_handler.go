package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

type SearchHandler struct {
	searchService ports.SearchService
	s3Service     s3.S3Service
}

func NewSearchHandler(searchService ports.SearchService, s3Service s3.S3Service) *SearchHandler {
	return &SearchHandler{
		searchService: searchService,
		s3Service:     s3Service,
	}
}

type SearchRequest struct {
	Query   string `json:"query"`
	Type    string `json:"type"`
	Country string `json:"country"`
}

func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	currentUserID, _ := r.Context().Value(UserIDKey).(string)
	results, err := h.searchService.Search(r.Context(), req.Query, req.Type, req.Country, currentUserID)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	switch res := results.(type) {
	case []domain.Event:
		// Event banners are stored as S3 keys; clients need loadable URLs.
		resolveEventImages(r.Context(), h.s3Service, res)
	case []domain.User:
		// Search is public: never expose email, dob, document, role or follower ID lists.
		results = domain.PublicUsers(res)
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}
