package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"time"

	"github.com/google/uuid"
)

type transferService struct {
	transferRepo ports.TransferRepository
	chatRepo     ports.ChatRepository
	messageRepo  ports.MessageRepository
}

func NewTransferService(
	transferRepo ports.TransferRepository,
	chatRepo ports.ChatRepository,
	messageRepo ports.MessageRepository,
) ports.TransferService {
	return &transferService{
		transferRepo: transferRepo,
		chatRepo:     chatRepo,
		messageRepo:  messageRepo,
	}
}

func (s *transferService) CreateTransfer(ctx context.Context, eventID, sellerID string, price float64) (*domain.Transfer, error) {
	transfer := &domain.Transfer{
		ID:          uuid.New().String(),
		EventID:     eventID,
		SellerID:    sellerID,
		BuyerID:     nil,
		ChatID:      nil,
		Status:      domain.TransferStatusAvailable,
		PriceAgreed: price,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.transferRepo.CreateTransfer(ctx, transfer); err != nil {
		return nil, err
	}

	return transfer, nil
}

func (s *transferService) GetTransfers(ctx context.Context, status *string) ([]domain.Transfer, error) {
	return s.transferRepo.GetTransfers(ctx, status)
}

func (s *transferService) StartDeal(ctx context.Context, transferID, buyerID string) error {
	transfer, err := s.transferRepo.GetTransferByID(ctx, transferID)
	if err != nil {
		return err
	}

	if transfer.Status != domain.TransferStatusAvailable || transfer.BuyerID != nil {
		return errors.New("transfer is not available")
	}

	chatID := uuid.New().String()
	chat := &domain.Chat{
		ID:        chatID,
		Type:      domain.ChatTypeTransfer,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.chatRepo.CreateChat(ctx, chat); err != nil {
		return err
	}
	if err := s.chatRepo.AddParticipant(ctx, &domain.ChatParticipant{ChatID: chatID, UserID: transfer.SellerID, JoinedAt: time.Now()}); err != nil {
		return err
	}
	if err := s.chatRepo.AddParticipant(ctx, &domain.ChatParticipant{ChatID: chatID, UserID: buyerID, JoinedAt: time.Now()}); err != nil {
		return err
	}

	return s.transferRepo.UpdateStartDeal(ctx, transferID, buyerID, chatID)
}

func (s *transferService) GetTransfer(ctx context.Context, transferID, currentUserID string) (*domain.Transfer, error) {
	transfer, err := s.transferRepo.GetTransferByID(ctx, transferID)
	if err != nil {
		return nil, err
	}

	// Validate user is part of the transfer (either seller or buyer)
	if transfer.SellerID != currentUserID && (transfer.BuyerID == nil || *transfer.BuyerID != currentUserID) {
		return nil, errors.New("unauthorized: user is not part of this transfer")
	}

	return transfer, nil
}

func (s *transferService) UpdateTransferStatus(ctx context.Context, transferID, currentUserID string, status domain.TransferStatus, ticketURL *string) error {
	transfer, err := s.transferRepo.GetTransferByID(ctx, transferID)
	if err != nil {
		return err
	}

	// Validate authorization
	if transfer.SellerID != currentUserID && (transfer.BuyerID == nil || *transfer.BuyerID != currentUserID) {
		return errors.New("unauthorized")
	}

	if err := s.transferRepo.UpdateStatus(ctx, transferID, status, ticketURL); err != nil {
		return err
	}

	// Emitir un mensaje de sistema en el chat basado en el nuevo estado
	if transfer.ChatID != nil {
		var content string
		switch status {
		case domain.TransferStatusTicketSent:
			content = "TICKET_SENT"
		case domain.TransferStatusCompleted:
			content = "COMPLETED"
		case domain.TransferStatusDisputed:
			content = "DISPUTED"
		case domain.TransferStatusCancelled:
			content = "CANCELLED"
		}

		if content != "" {
			msg := &domain.Message{
				ID:        uuid.New().String(),
				ChatID:    *transfer.ChatID,
				SenderID:  "00000000-0000-0000-0000-000000000000",
				Content:   content,
				Type:      domain.MessageTypeSystem,
				Metadata:  []byte(`{"event_id":"` + transfer.EventID + `","status":"` + string(status) + `"}`),
				Timestamp: time.Now(),
			}
			_ = s.messageRepo.CreateMessage(ctx, msg)
			_ = s.chatRepo.UpdateLastMessage(ctx, *transfer.ChatID, "[Actualización del Trato]")
		}
	}

	return nil
}

func (s *transferService) CreatePaymentPreference(ctx context.Context, transferID, buyerID string) (string, error) {
	transfer, err := s.transferRepo.GetTransferByID(ctx, transferID)
	if err != nil {
		return "", err
	}
	if transfer.BuyerID == nil || *transfer.BuyerID != buyerID {
		return "", errors.New("unauthorized: user is not the buyer of this transfer")
	}
	if transfer.Status != domain.TransferStatusNegotiating && transfer.Status != domain.TransferStatusTicketSent {
		return "", errors.New("transfer is not in a valid status to pay")
	}

	accessToken := strings.TrimSpace(os.Getenv("MERCADOPAGO_ACCESS_TOKEN"))
	publicAPIURL := os.Getenv("PUBLIC_API_URL")

	if accessToken == "" || publicAPIURL == "" {
		return "", errors.New("mercadopago integration is not configured")
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:3000" // Default for local dev
	}

	payload := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("Transferencia - Evento %s", transfer.EventID),
				"quantity":    1,
				"unit_price":  transfer.PriceAgreed,
				"currency_id": "ARS",
			},
		},
		"back_urls": map[string]string{
			"success": fmt.Sprintf("%s/transfers/%s?status=success", frontendURL, transferID),
			"failure": fmt.Sprintf("%s/transfers/%s?status=failure", frontendURL, transferID),
			"pending": fmt.Sprintf("%s/transfers/%s?status=pending", frontendURL, transferID),
		},
		"external_reference": transferID,
		"notification_url":   fmt.Sprintf("%s/v1/webhooks/mercadopago", publicAPIURL),
	}

	// Mercado Pago requires back_urls to be HTTPS if auto_return is enabled
	if len(frontendURL) >= 5 && frontendURL[:5] == "https" {
		payload["auto_return"] = "approved"
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.mercadopago.com/checkout/preferences", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	fmt.Println("🚀 MERCADOPAGO PAYLOAD:", string(bodyBytes))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("mercadopago error: status %d - body: %s", resp.StatusCode, string(b))
	}

	var mpResp struct {
		InitPoint string `json:"init_point"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mpResp); err != nil {
		return "", err
	}

	return mpResp.InitPoint, nil
}

func (s *transferService) HandleMercadoPagoWebhook(ctx context.Context, topic, id string) error {
	if topic != "payment" {
		return nil // Ignore other topics for now
	}

	accessToken := os.Getenv("MERCADOPAGO_ACCESS_TOKEN")
	if accessToken == "" {
		return errors.New("mercadopago integration is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.mercadopago.com/v1/payments/%s", id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mercadopago error: status %d", resp.StatusCode)
	}

	var paymentResp struct {
		Status            string `json:"status"`
		ExternalReference string `json:"external_reference"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paymentResp); err != nil {
		return err
	}

	if paymentResp.Status != "approved" {
		return nil // Payment not approved yet
	}

	transferID := paymentResp.ExternalReference
	if transferID == "" {
		return errors.New("no external_reference in payment")
	}

	transfer, err := s.transferRepo.GetTransferByID(ctx, transferID)
	if err != nil {
		return err
	}

	if transfer.Status == domain.TransferStatusPaid {
		return nil // Already paid
	}

	// Update status to PAID
	if err := s.transferRepo.UpdateStatus(ctx, transferID, domain.TransferStatusPaid, nil); err != nil {
		return err
	}

	// Emit system message for PAID
	if transfer.ChatID != nil {
		msg := &domain.Message{
			ID:        uuid.New().String(),
			ChatID:    *transfer.ChatID,
			SenderID:  "00000000-0000-0000-0000-000000000000",
			Content:   "PAID",
			Type:      domain.MessageTypeSystem,
			Metadata:  []byte(`{"event_id":"` + transfer.EventID + `","status":"PAID"}`),
			Timestamp: time.Now(),
		}
		_ = s.messageRepo.CreateMessage(ctx, msg)
		_ = s.chatRepo.UpdateLastMessage(ctx, *transfer.ChatID, "[Pago Confirmado]")
	}

	return nil
}
