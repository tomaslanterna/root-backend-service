package domain

import "time"

type MessageType string

const (
	MessageTypeText   MessageType = "text"
	MessageTypeImage  MessageType = "image"
	MessageTypeSystem MessageType = "system"
)

type Message struct {
	ID        string      `json:"id"`
	ChatID    string      `json:"chat_id"`
	SenderID  string      `json:"sender_id"`
	Content   string      `json:"content"`
	Type      MessageType `json:"type"`
	Metadata  interface{} `json:"metadata"`
	Timestamp time.Time   `json:"timestamp"`
}
