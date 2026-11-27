package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
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

func (h *TransferHandler) PayTransfer(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "id")
	currentUserID := r.Context().Value(UserIDKey).(string)

	initPoint, err := h.transferService.CreatePaymentPreference(r.Context(), transferID, currentUserID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"init_point": initPoint})
}

// mercadoPagoPaymentID: los ids de pago de MercadoPago son numéricos. Cualquier otra cosa se rechaza
// antes de armar la URL de consulta.
var mercadoPagoPaymentID = regexp.MustCompile(`^[0-9]{1,30}$`)

func (h *TransferHandler) MercadoPagoWebhook(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	id := r.URL.Query().Get("id")

	// Mercado pago sometimes sends them in body for v1 webhooks if not in query
	if topic == "" || id == "" {
		topic = r.URL.Query().Get("type")
		id = r.URL.Query().Get("data.id")
	}

	if topic == "" || id == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !mercadoPagoPaymentID.MatchString(id) {
		respondWithError(w, http.StatusBadRequest, "id inválido")
		return
	}
	// Con MERCADOPAGO_WEBHOOK_SECRET configurado, solo se aceptan notificaciones firmadas por MercadoPago.
	// Sin el secreto se sigue aceptando (el pago igual se vuelve a consultar a MercadoPago antes de marcarlo).
	if secret := os.Getenv("MERCADOPAGO_WEBHOOK_SECRET"); secret != "" {
		if !validMercadoPagoSignature(r.Header.Get("x-signature"), r.Header.Get("x-request-id"), id, secret) {
			respondWithError(w, http.StatusUnauthorized, "firma inválida")
			return
		}
	}

	if err := h.transferService.HandleMercadoPagoWebhook(r.Context(), topic, id); err != nil {
		// El detalle queda en el log; al que llama no se le devuelven errores internos.
		log.Printf("MercadoPago webhook %s %s failed: %v", topic, id, err)
		respondWithError(w, http.StatusInternalServerError, "No se pudo procesar la notificación")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// validMercadoPagoSignature valida el header x-signature ("ts=...,v1=...") según la documentación de
// MercadoPago: HMAC-SHA256 en hex de "id:<data.id>;request-id:<x-request-id>;ts:<ts>;".
func validMercadoPagoSignature(signature, requestID, dataID, secret string) bool {
	var ts, v1 string
	for _, part := range strings.Split(signature, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ts":
			ts = strings.TrimSpace(value)
		case "v1":
			v1 = strings.TrimSpace(value)
		}
	}
	if ts == "" || v1 == "" {
		return false
	}
	manifest := "id:" + strings.ToLower(dataID) + ";"
	if requestID != "" {
		manifest += "request-id:" + requestID + ";"
	}
	manifest += "ts:" + ts + ";"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(manifest))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(v1)))
}
