package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"time"
)

type messageRepository struct{ db *sql.DB }

func NewMessageRepository(db *sql.DB) ports.MessageRepository { return &messageRepository{db: db} }

func (r *messageRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS messages_chat_timestamp_id_idx ON messages(chat_id, timestamp, id)`,
		`CREATE TABLE IF NOT EXISTS message_receipts (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), read_at TIMESTAMPTZ,
    PRIMARY KEY(message_id, user_id))`,
		// The old read_at was global. Only direct chats can safely retain that receipt.
		`INSERT INTO message_receipts(message_id,user_id,delivered_at,read_at)
   SELECT m.id,cp.user_id,m.read_at,m.read_at FROM messages m
   JOIN chats c ON c.id=m.chat_id AND c.type='DIRECT'
   JOIN chat_participants cp ON cp.chat_id=m.chat_id AND cp.user_id<>m.sender_id
   WHERE m.read_at IS NOT NULL ON CONFLICT DO NOTHING`,
		`CREATE OR REPLACE FUNCTION root_chat_message_notify() RETURNS trigger AS $$
   BEGIN
    PERFORM pg_notify('root_chat_events', json_build_object('chat_id',NEW.chat_id,'message_id',NEW.id,'type','message.created')::text);
    RETURN NEW;
   END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS root_chat_message_notify ON messages`,
		`CREATE TRIGGER root_chat_message_notify AFTER INSERT ON messages FOR EACH ROW EXECUTE FUNCTION root_chat_message_notify()`,
		`CREATE OR REPLACE FUNCTION root_chat_receipt_notify() RETURNS trigger AS $$
   DECLARE cid UUID;
   BEGIN
    SELECT chat_id INTO cid FROM messages WHERE id=NEW.message_id;
    PERFORM pg_notify('root_chat_events',json_build_object('chat_id',cid,'message_id',NEW.message_id,'type','message.updated')::text);
    RETURN NEW;
   END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS root_chat_receipt_notify ON message_receipts`,
		`CREATE TRIGGER root_chat_receipt_notify AFTER INSERT OR UPDATE ON message_receipts FOR EACH ROW EXECUTE FUNCTION root_chat_receipt_notify()`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("message migration: %w", err)
		}
	}
	return tx.Commit()
}

// Insert and conversation preview update commit together. The UUID is an idempotency key.
func (r *messageRepository) CreateMessage(ctx context.Context, msg *domain.Message) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var metadata []byte
	if msg.Metadata != nil {
		metadata, err = json.Marshal(msg.Metadata)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(id,chat_id,sender_id,content,type,metadata,timestamp)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, msg.ID, msg.ChatID, msg.SenderID, msg.Content, msg.Type, metadata, msg.Timestamp)
	if err != nil {
		return fmt.Errorf("creating message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var chatID, senderID, content string
		var kind domain.MessageType
		err = tx.QueryRowContext(ctx, `SELECT chat_id,sender_id,content,type,timestamp FROM messages WHERE id=$1`, msg.ID).Scan(&chatID, &senderID, &content, &kind, &msg.Timestamp)
		if err != nil {
			return err
		}
		if chatID != msg.ChatID || senderID != msg.SenderID || content != msg.Content || kind != msg.Type {
			return errors.New("client_message_id already used")
		}
	} else {
		// Return the database's timestamp precision, not the pre-insert clock value.
		if err := tx.QueryRowContext(ctx, `SELECT timestamp FROM messages WHERE id=$1`, msg.ID).Scan(&msg.Timestamp); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE chats SET last_message=$2,updated_at=$3 WHERE id=$1 AND updated_at<=$3`, msg.ChatID, msg.Content, msg.Timestamp)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	canonical, err := r.GetMessagesByChatID(ctx, msg.ChatID, "id:"+msg.ID, msg.SenderID)
	if err != nil {
		return err
	}
	if len(canonical) == 1 {
		*msg = canonical[0]
	}
	return nil
}

// History selects the newest page and returns it chronologically.
// before:<RFC3339>|<UUID> is a stable keyset cursor; id:<UUID> is used by realtime.
func (r *messageRepository) GetMessagesByChatID(ctx context.Context, chatID, cursor, userID string) ([]domain.Message, error) {
	condition := ""
	args := []interface{}{chatID, userID}
	if strings.HasPrefix(cursor, "id:") {
		condition = " AND m.id=$3"
		args = append(args, strings.TrimPrefix(cursor, "id:"))
	} else if strings.HasPrefix(cursor, "before:") {
		parts := strings.Split(strings.TrimPrefix(cursor, "before:"), "|")
		if len(parts) != 2 {
			return nil, errors.New("invalid message cursor")
		}
		if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
			return nil, errors.New("invalid message cursor")
		}
		condition = " AND (m.timestamp,m.id)<($3::timestamptz,$4::uuid)"
		args = append(args, parts[0], parts[1])
	} else if cursor != "" {
		if _, err := time.Parse(time.RFC3339Nano, cursor); err != nil {
			return nil, errors.New("invalid message cursor")
		}
		condition = " AND m.timestamp>=$3::timestamptz"
		args = append(args, cursor)
	}
	query := `SELECT m.id,m.chat_id,m.sender_id,m.content,m.type,m.metadata,m.timestamp,
 CASE WHEN m.sender_id=$2 THEN
   CASE WHEN EXISTS(SELECT 1 FROM chat_participants cp WHERE cp.chat_id=m.chat_id AND cp.user_id<>m.sender_id)
     AND NOT EXISTS(SELECT 1 FROM chat_participants cp LEFT JOIN message_receipts mr ON mr.message_id=m.id AND mr.user_id=cp.user_id
       WHERE cp.chat_id=m.chat_id AND cp.user_id<>m.sender_id AND mr.read_at IS NULL) THEN 'read'
   WHEN EXISTS(SELECT 1 FROM chat_participants cp WHERE cp.chat_id=m.chat_id AND cp.user_id<>m.sender_id)
     AND NOT EXISTS(SELECT 1 FROM chat_participants cp LEFT JOIN message_receipts mr ON mr.message_id=m.id AND mr.user_id=cp.user_id
       WHERE cp.chat_id=m.chat_id AND cp.user_id<>m.sender_id AND mr.delivered_at IS NULL) THEN 'delivered'
   ELSE 'sent' END
 ELSE CASE WHEN receipt.read_at IS NOT NULL THEN 'read' WHEN receipt.delivered_at IS NOT NULL THEN 'delivered' ELSE 'sent' END END,
 CASE WHEN m.sender_id=$2 THEN (SELECT MAX(read_at) FROM message_receipts WHERE message_id=m.id) ELSE receipt.read_at END,
 CASE WHEN m.sender_id=$2 THEN (SELECT MAX(delivered_at) FROM message_receipts WHERE message_id=m.id) ELSE receipt.delivered_at END
 FROM messages m LEFT JOIN message_receipts receipt ON receipt.message_id=m.id AND receipt.user_id=$2
 WHERE m.chat_id=$1` + condition + ` ORDER BY m.timestamp DESC,m.id DESC LIMIT 50`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()
	messages := make([]domain.Message, 0)
	for rows.Next() {
		var msg domain.Message
		var metadata []byte
		if err := rows.Scan(&msg.ID, &msg.ChatID, &msg.SenderID, &msg.Content, &msg.Type, &metadata, &msg.Timestamp, &msg.Status, &msg.ReadAt, &msg.DeliveredAt); err != nil {
			return nil, err
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &msg.Metadata); err != nil {
				return nil, err
			}
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

func (r *messageRepository) AcknowledgeMessages(ctx context.Context, chatID, userID string, ids []string, read bool) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO message_receipts(message_id,user_id,delivered_at,read_at)
 SELECT m.id,$2,NOW(),CASE WHEN $4 THEN NOW() END FROM messages m
 WHERE m.chat_id=$1 AND m.id=ANY($3::uuid[]) AND m.sender_id<>$2
 AND EXISTS(SELECT 1 FROM chat_participants WHERE chat_id=$1 AND user_id=$2)
 ON CONFLICT(message_id,user_id) DO UPDATE SET read_at=COALESCE(message_receipts.read_at,EXCLUDED.read_at)
 WHERE message_receipts.read_at IS NULL AND EXCLUDED.read_at IS NOT NULL`, chatID, userID, pq.Array(ids), read)
	if err != nil {
		return fmt.Errorf("acknowledging messages: %w", err)
	}
	return nil
}

func (r *messageRepository) MarkMessagesRead(ctx context.Context, chatID, userID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO message_receipts(message_id,user_id,delivered_at,read_at)
 SELECT id,$2,NOW(),NOW() FROM messages WHERE chat_id=$1 AND sender_id<>$2
 ON CONFLICT(message_id,user_id) DO UPDATE SET read_at=EXCLUDED.read_at WHERE message_receipts.read_at IS NULL`, chatID, userID)
	return err
}
