package handlers

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"root-backend-service/internal/core/services"
	"strings"
)

type PushHandler struct{ service *services.PushService }

func NewPushHandler(service *services.PushService) *PushHandler {
	return &PushHandler{service: service}
}
func (h *PushHandler) Status(w http.ResponseWriter, r *http.Request) {
	respondWithJSON(w, http.StatusOK, map[string]bool{"enabled": h.service.Enabled()})
}

func (h *PushHandler) Register(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, 401, "Unauthorized")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondWithError(w, 400, "Invalid installation ID")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Platform != "android" || len(body.Token) < 20 || len(body.Token) > 4096 || strings.ContainsAny(body.Token, " \t\r\n") {
		respondWithError(w, 400, "Invalid Android push registration")
		return
	}
	if !h.service.Enabled() {
		respondWithError(w, 503, "Push notifications are not configured")
		return
	}
	if err := h.service.RegisterDevice(r.Context(), userID, id.String(), body.Token); err != nil {
		log.Printf("push registration failed")
		respondWithError(w, 500, "Could not register device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PushHandler) Remove(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithError(w, 401, "Unauthorized")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondWithError(w, 400, "Invalid installation ID")
		return
	}
	if err := h.service.RemoveDevice(r.Context(), userID, id.String()); err != nil {
		log.Printf("push removal failed")
		respondWithError(w, 500, "Could not remove device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
