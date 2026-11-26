package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

// ModerationHandler expone la baja de cuenta, los bloqueos y las denuncias generales.
type ModerationHandler struct {
	accounts ports.AccountService
	blocks   ports.BlockService
	reports  ports.ReportService
	storage  s3.S3Service
}

func NewModerationHandler(accounts ports.AccountService, blocks ports.BlockService, reports ports.ReportService, storage s3.S3Service) *ModerationHandler {
	return &ModerationHandler{accounts: accounts, blocks: blocks, reports: reports, storage: storage}
}

func currentUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return userID, true
}

// DeleteMe: DELETE /v1/users/me. Da de baja la cuenta al instante; los datos se purgan a los 30 días.
func (h *ModerationHandler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	err := h.accounts.DeleteAccount(r.Context(), userID)
	if errors.Is(err, domain.ErrUserNotFound) {
		respondWithError(w, http.StatusNotFound, "La cuenta no existe o ya fue eliminada")
		return
	}
	if err != nil {
		log.Printf("delete account %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo eliminar la cuenta. Intentá nuevamente.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Block: POST /v1/users/{username}/block.
func (h *ModerationHandler) Block(w http.ResponseWriter, r *http.Request) {
	h.changeBlock(w, r, h.blocks.Block)
}

// Unblock: DELETE /v1/users/{username}/block.
func (h *ModerationHandler) Unblock(w http.ResponseWriter, r *http.Request) {
	h.changeBlock(w, r, h.blocks.Unblock)
}

func (h *ModerationHandler) changeBlock(w http.ResponseWriter, r *http.Request, change func(ctx context.Context, blockerID, username string) error) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	err := change(r.Context(), userID, chi.URLParam(r, "username"))
	switch {
	case errors.Is(err, domain.ErrUserNotFound):
		respondWithError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, domain.ErrCannotBlockSelf):
		respondWithError(w, http.StatusBadRequest, "No podés bloquearte a vos mismo")
	case err != nil:
		log.Printf("block change by %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo actualizar el bloqueo. Intentá nuevamente.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListBlocks: GET /v1/users/me/blocks.
func (h *ModerationHandler) ListBlocks(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	users, err := h.blocks.List(r.Context(), userID)
	if err != nil {
		log.Printf("list blocks of %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudieron cargar los usuarios bloqueados")
		return
	}
	for i := range users {
		avatar := users[i].AvatarURL
		if h.storage == nil || avatar == nil || *avatar == "" || strings.HasPrefix(*avatar, "http") {
			continue
		}
		if url, err := h.storage.GenerateViewUrl(r.Context(), *avatar, 7*24*time.Hour); err == nil {
			*avatar = url
		}
	}
	respondWithJSON(w, http.StatusOK, map[string]any{"data": users})
}

// CreateReport: POST /v1/reports con {targetType, targetId, reason, details?}. 201 si es nueva, 200 si ya existía.
func (h *ModerationHandler) CreateReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input domain.ReportInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "Denuncia inválida")
		return
	}
	id, created, err := h.reports.Create(r.Context(), userID, input)
	switch {
	case errors.Is(err, domain.ErrInvalidReport):
		respondWithError(w, http.StatusBadRequest, "Denuncia inválida")
	case errors.Is(err, domain.ErrReportTargetNotFound):
		respondWithError(w, http.StatusNotFound, "No encontramos ese contenido")
	case err != nil:
		log.Printf("create report by %s: %v", userID, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo enviar la denuncia. Intentá nuevamente.")
	case created:
		respondWithJSON(w, http.StatusCreated, map[string]string{"id": id})
	default:
		respondWithJSON(w, http.StatusOK, map[string]string{"id": id})
	}
}
