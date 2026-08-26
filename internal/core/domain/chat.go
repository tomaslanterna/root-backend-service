package domain

import "time"

type ChatType string

const (
	ChatTypeDirect   ChatType = "DIRECT"
	ChatTypeTransfer ChatType = "TRANSFER"
	ChatTypeCrews    ChatType = "CREWS"
)

type Chat struct {
	ID          string    `json:"id"`
	Type        ChatType  `json:"type"`
	LastMessage string    `json:"last_message"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ChatParticipant struct {
	ChatID   string    `json:"chat_id"`
	UserID   string    `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}
