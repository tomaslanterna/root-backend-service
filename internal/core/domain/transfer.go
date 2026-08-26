package domain

import "time"

type TransferStatus string

const (
	TransferStatusAvailable   TransferStatus = "AVAILABLE"
	TransferStatusNegotiating TransferStatus = "NEGOTIATING"
	TransferStatusTicketSent  TransferStatus = "TICKET_SENT"
	TransferStatusCompleted   TransferStatus = "COMPLETED"
	TransferStatusDisputed    TransferStatus = "DISPUTED"
	TransferStatusCancelled   TransferStatus = "CANCELLED"
)

type Transfer struct {
	ID            string         `json:"id"`
	EventID       string         `json:"event_id"`
	SellerID      string         `json:"seller_id"`
	BuyerID       *string        `json:"buyer_id,omitempty"`
	ChatID        *string        `json:"chat_id,omitempty"`
	TicketFileURL *string        `json:"ticket_file_url,omitempty"`
	Status        TransferStatus `json:"status"`
	PriceAgreed   float64        `json:"price_agreed"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
