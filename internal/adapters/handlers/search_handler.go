package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/ports"
)

type SearchHandler struct {
	searchService ports.SearchService
}

func NewSearchHandler(searchService ports.SearchService) *SearchHandler {
	return &SearchHandler{
		searchService: searchService,
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

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}
