package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type messageRepository struct {
	db *sql.DB
}

func NewMessageRepository(db *sql.DB) ports.MessageRepository {
	return &messageRepository{db: db}
}

func (r *messageRepository) CreateMessage(ctx context.Context, msg *domain.Message) error {
	query := `
		INSERT INTO messages (id, chat_id, sender_id, content, type, metadata, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	
	var metadataJSON []byte
	if msg.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(msg.Metadata)
		if err != nil {
			return err
		}
	}

	_, err := r.db.ExecContext(ctx, query,
		msg.ID, msg.ChatID, msg.SenderID, msg.Content, msg.Type, metadataJSON, msg.Timestamp,
	)
	return err
}

func (r *messageRepository) GetMessagesByChatID(ctx context.Context, chatID string, afterTimestamp string) ([]domain.Message, error) {
	query := `
		SELECT id, chat_id, sender_id, content, type, metadata, timestamp
		FROM messages
		WHERE chat_id = $1
	`
	args := []interface{}{chatID}

	if afterTimestamp != "" {
		query += ` AND timestamp > $2`
		args = append(args, afterTimestamp)
	}

	query += ` ORDER BY timestamp ASC LIMIT 50`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var msg domain.Message
		var metadataJSON []byte
		err := rows.Scan(
			&msg.ID, &msg.ChatID, &msg.SenderID, &msg.Content, &msg.Type, &metadataJSON, &msg.Timestamp,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			var meta interface{}
			if err := json.Unmarshal(metadataJSON, &meta); err == nil {
				msg.Metadata = meta
			}
		}

		messages = append(messages, msg)
	}
	return messages, nil
}
