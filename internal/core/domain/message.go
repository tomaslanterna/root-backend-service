package domain

import "time"

type MessageType string
type MessageStatus string

const (
	MessageTypeText   MessageType = "text"
	MessageTypeImage  MessageType = "image"
	MessageTypeSystem MessageType = "system"

	MessageStatusSent      MessageStatus = "sent"
	MessageStatusDelivered MessageStatus = "delivered"
	MessageStatusRead      MessageStatus = "read"
)

type Message struct {
	ID          string        `json:"id"`
	ChatID      string        `json:"chat_id"`
	SenderID    string        `json:"sender_id"`
	Content     string        `json:"content"`
	Type        MessageType   `json:"type"`
	Metadata    interface{}   `json:"metadata"`
	Timestamp   time.Time     `json:"timestamp"`
	Status      MessageStatus `json:"status"`
	ReadAt      *time.Time    `json:"read_at,omitempty"`
	DeliveredAt *time.Time    `json:"delivered_at,omitempty"`
}
