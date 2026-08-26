package postgres

import (
	"context"
	"database/sql"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type chatRepository struct {
	db *sql.DB
}

func NewChatRepository(db *sql.DB) ports.ChatRepository {
	return &chatRepository{db: db}
}

func (r *chatRepository) CreateChat(ctx context.Context, chat *domain.Chat) error {
	query := `
		INSERT INTO chats (id, type, last_message, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query,
		chat.ID, chat.Type, chat.LastMessage, chat.CreatedAt, chat.UpdatedAt,
	)
	return err
}

func (r *chatRepository) GetChatByID(ctx context.Context, id string) (*domain.Chat, error) {
	query := `
		SELECT id, type, last_message, created_at, updated_at
		FROM chats
		WHERE id = $1
	`
	var chat domain.Chat
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&chat.ID, &chat.Type, &chat.LastMessage, &chat.CreatedAt, &chat.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("chat not found")
		}
		return nil, err
	}
	return &chat, nil
}

func (r *chatRepository) UpdateLastMessage(ctx context.Context, chatID string, lastMessage string) error {
	query := `
		UPDATE chats
		SET last_message = $1, updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, lastMessage, chatID)
	return err
}

func (r *chatRepository) AddParticipant(ctx context.Context, participant *domain.ChatParticipant) error {
	query := `
		INSERT INTO chat_participants (chat_id, user_id, joined_at)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`
	_, err := r.db.ExecContext(ctx, query,
		participant.ChatID, participant.UserID, participant.JoinedAt,
	)
	return err
}
