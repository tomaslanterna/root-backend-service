package handlers

import (
	"encoding/json"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type TransferHandler struct {
	transferService ports.TransferService
}

func NewTransferHandler(transferService ports.TransferService) *TransferHandler {
	return &TransferHandler{
		transferService: transferService,
	}
}

type CreateTransferRequest struct {
	EventID string  `json:"event_id"`
	Price   float64 `json:"price"`
}

func (h *TransferHandler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	currentUserID := r.Context().Value(UserIDKey).(string)

	var req CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	// currentUserID acts as the Seller
	transfer, err := h.transferService.CreateTransfer(r.Context(), req.EventID, currentUserID, req.Price)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusCreated, transfer)
}

func (h *TransferHandler) GetTransfers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	
	transfers, err := h.transferService.GetTransfers(r.Context(), statusPtr)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, transfers)
}

func (h *TransferHandler) StartDeal(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	err := h.transferService.StartDeal(r.Context(), transferID, currentUserID)
	if err != nil {
		if err.Error() == "transfer is not available" || err.Error() == "conflict: transfer not available" {
			respondWithError(w, http.StatusConflict, err.Error())
			return
		}
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Deal started"})
}

func (h *TransferHandler) GetTransfer(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	transfer, err := h.transferService.GetTransfer(r.Context(), transferID, currentUserID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Transfer not found or unauthorized")
		return
	}

	respondWithJSON(w, http.StatusOK, transfer)
}

type UpdateTransferStatusRequest struct {
	Status    domain.TransferStatus `json:"status"`
	TicketURL *string               `json:"ticket_file_url,omitempty"`
}

func (h *TransferHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	var req UpdateTransferStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	err := h.transferService.UpdateTransferStatus(r.Context(), transferID, currentUserID, req.Status, req.TicketURL)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "success"})
}
