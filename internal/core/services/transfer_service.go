package services

import (
	"context"
	"errors"
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
