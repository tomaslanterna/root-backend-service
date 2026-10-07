package handlers

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"root-backend-service/internal/core/domain"
)

func communityAuth(w http.ResponseWriter, r *http.Request) string {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, 401, "Usuario no autenticado")
	}
	return userID
}

func communityBody(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		respondWithError(w, 400, "JSON inválido")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		respondWithError(w, 400, "JSON inválido")
		return false
	}
	return true
}

func communityResult(w http.ResponseWriter, err error) {
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, 200, map[string]bool{"ok": true})
}

func (h *CommunityHandler) SetMuted(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Muted *bool `json:"muted"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	if body.Muted == nil {
		respondWithError(w, 400, "muted es obligatorio")
		return
	}
	communityResult(w, h.communityService.SetMuted(r.Context(), chi.URLParam(r, "id"), u, *body.Muted))
}
func (h *CommunityHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		PostID string `json:"postId"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	communityResult(w, h.communityService.MarkRead(r.Context(), chi.URLParam(r, "id"), u, body.PostID))
}
func (h *CommunityHandler) SetPinned(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	if body.Pinned == nil {
		respondWithError(w, 400, "pinned es obligatorio")
		return
	}
	communityResult(w, h.communityService.SetPinned(r.Context(), chi.URLParam(r, "id"), u, chi.URLParam(r, "postID"), *body.Pinned))
}
func (h *CommunityHandler) CreateReport(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body domain.CommunityReportInput
	if !communityBody(w, r, &body) {
		return
	}
	id, err := h.communityService.CreateReport(r.Context(), chi.URLParam(r, "id"), u, body)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, 201, map[string]string{"id": id})
}
func (h *CommunityHandler) GetReports(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	limit, offset, err := parseCommunityPagination(r, 10)
	if err != nil {
		respondWithError(w, 400, err.Error())
		return
	}
	data, total, err := h.communityService.GetReports(r.Context(), chi.URLParam(r, "id"), u, limit, offset)
	if err != nil {
		respondWithCommunityError(w, err)
		return
	}
	respondWithJSON(w, 200, map[string]any{"data": data, "meta": map[string]any{"total": total, "limit": limit, "offset": offset, "hasMore": offset+len(data) < total}})
}
func (h *CommunityHandler) ReviewReport(w http.ResponseWriter, r *http.Request) {
	u := communityAuth(w, r)
	if u == "" {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !communityBody(w, r, &body) {
		return
	}
	communityResult(w, h.communityService.ReviewReport(r.Context(), chi.URLParam(r, "id"), u, chi.URLParam(r, "reportID"), body.Status))
}
