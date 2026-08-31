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
	
	participants, err := r.getChatParticipants(ctx, chat.ID)
	if err == nil {
		chat.Participants = participants
	} else {
		// Log the error to debug why participants failed to load
		// log.Printf("ERROR getting participants for chat %s: %v", chat.ID, err)
		return nil, err // Return the error so it doesn't silently fail and cause authorization issues
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

func (r *chatRepository) GetUserChats(ctx context.Context, userID string) ([]domain.Chat, error) {
	query := `
		SELECT c.id, c.type, c.last_message, c.created_at, c.updated_at
		FROM chats c
		JOIN chat_participants cp ON c.id = cp.chat_id
		WHERE cp.user_id = $1
		ORDER BY c.updated_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []domain.Chat
	for rows.Next() {
		var chat domain.Chat
		if err := rows.Scan(&chat.ID, &chat.Type, &chat.LastMessage, &chat.CreatedAt, &chat.UpdatedAt); err != nil {
			return nil, err
		}
		
		// Get participants for each chat
		participants, err := r.getChatParticipants(ctx, chat.ID)
		if err == nil {
			chat.Participants = participants
		}
		
		chats = append(chats, chat)
	}
	return chats, nil
}

func (r *chatRepository) GetDirectChatBetweenUsers(ctx context.Context, user1ID, user2ID string) (*domain.Chat, error) {
	query := `
		SELECT c.id, c.type, c.last_message, c.created_at, c.updated_at
		FROM chats c
		JOIN chat_participants cp1 ON c.id = cp1.chat_id
		JOIN chat_participants cp2 ON c.id = cp2.chat_id
		WHERE c.type = 'DIRECT' AND cp1.user_id = $1 AND cp2.user_id = $2
		LIMIT 1
	`
	var chat domain.Chat
	err := r.db.QueryRowContext(ctx, query, user1ID, user2ID).Scan(
		&chat.ID, &chat.Type, &chat.LastMessage, &chat.CreatedAt, &chat.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Return nil if not found
		}
		return nil, err
	}
	
	participants, err := r.getChatParticipants(ctx, chat.ID)
	if err == nil {
		chat.Participants = participants
	}
	
	return &chat, nil
}

func (r *chatRepository) getChatParticipants(ctx context.Context, chatID string) ([]domain.User, error) {
	query := `
		SELECT u.id, u.email, u.name, u.username, u.role, u.avatar_url, u.is_kyc_verified
		FROM users u
		JOIN chat_participants cp ON u.id = cp.user_id
		WHERE cp.chat_id = $1
	`
	rows, err := r.db.QueryContext(ctx, query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Username, &u.Role, &u.AvatarURL, &u.IsKycVerified); err != nil {
			return nil, err
		}
		participants = append(participants, u)
	}
	return participants, nil
}
