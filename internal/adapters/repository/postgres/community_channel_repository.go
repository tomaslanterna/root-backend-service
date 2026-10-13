package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
)

func (r *communityRepository) GetChatSettings(ctx context.Context, communityID string) (*domain.CommunityChatSettings, error) {
	settings := &domain.CommunityChatSettings{
		CommunityID: communityID,
		Mode:        "broadcast_only",
		UpdatedAt:   time.Now(),
	}

	var updatedBy sql.NullString
	var updatedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT mode, updated_at, updated_by
		FROM community_chat_settings
		WHERE community_id = $1::uuid`, communityID).Scan(&settings.Mode, &updatedAt, &updatedBy)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get chat settings: %w", err)
	}

	if updatedAt.Valid {
		settings.UpdatedAt = updatedAt.Time
	}
	if updatedBy.Valid {
		settings.UpdatedBy = &updatedBy.String
	}

	return settings, nil
}

func (r *communityRepository) UpdateChatSettings(ctx context.Context, communityID, userID, mode string) (*domain.CommunityChatSettings, error) {
	settings := &domain.CommunityChatSettings{
		CommunityID: communityID,
		Mode:        mode,
	}

	var updatedBy sql.NullString
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO community_chat_settings (community_id, mode, updated_at, updated_by)
		VALUES ($1::uuid, $2, NOW(), $3::uuid)
		ON CONFLICT (community_id) DO UPDATE
		SET mode = EXCLUDED.mode, updated_at = NOW(), updated_by = EXCLUDED.updated_by
		RETURNING mode, updated_at, updated_by`, communityID, mode, userID).Scan(&settings.Mode, &settings.UpdatedAt, &updatedBy)
	if err != nil {
		return nil, fmt.Errorf("update chat settings: %w", err)
	}
	if updatedBy.Valid {
		settings.UpdatedBy = &updatedBy.String
	}

	// Trigger real-time event for connected clients
	_, _ = r.db.ExecContext(ctx, `SELECT pg_notify('root_community_events', jsonb_build_object('type', 'community.chat_mode.updated', 'community_id', $1::text, 'mode', $2::text)::text)`, communityID, mode)

	return settings, nil
}

func (r *communityRepository) GetCommunityMessages(ctx context.Context, communityID string, limit int, before string) ([]domain.CommunityChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var condition string
	args := []interface{}{communityID}

	if before != "" {
		if strings.HasPrefix(before, "id:") {
			args = append(args, strings.TrimPrefix(before, "id:"))
			condition = fmt.Sprintf(" AND m.id = $%d::uuid", len(args))
		} else {
			parts := strings.Split(before, "|")
			t, err := time.Parse(time.RFC3339Nano, parts[0])
			if err != nil {
				t, err = time.Parse(time.RFC3339, parts[0])
			}
			if err == nil {
				args = append(args, t)
				if len(parts) > 1 && parts[1] != "" {
					args = append(args, parts[1])
					condition = fmt.Sprintf(" AND (m.timestamp < $%d OR (m.timestamp = $%d AND m.id < $%d::uuid))", len(args)-1, len(args)-1, len(args))
				} else {
					condition = fmt.Sprintf(" AND m.timestamp < $%d", len(args))
				}
			}
		}
	}

	query := fmt.Sprintf(`
		SELECT m.id, m.community_id, m.sender_id,
			COALESCE(u.name, 'Usuario'), COALESCE(u.username, ''), COALESCE(u.avatar_url, ''),
			CASE
				WHEN UPPER(u.role) = 'RRPP' OR c.pr_owner_id = u.id OR cmgr.user_id IS NOT NULL THEN 'RRPP'
				WHEN UPPER(u.role) = 'ADMIN' THEN 'ADMIN'
				ELSE 'MEMBER'
			END AS sender_role,
			m.content, m.timestamp
		FROM community_messages m
		JOIN communities c ON c.id = m.community_id
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN community_managers cmgr ON cmgr.community_id = m.community_id AND cmgr.user_id = u.id
		WHERE m.community_id = $1::uuid%s
		ORDER BY m.timestamp DESC, m.id DESC
		LIMIT %d`, condition, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query community messages: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.CommunityChatMessage, 0)
	msgIDs := make([]string, 0)

	for rows.Next() {
		var msg domain.CommunityChatMessage
		var username, avatar sql.NullString
		if err := rows.Scan(
			&msg.ID, &msg.CommunityID, &msg.SenderID,
			&msg.SenderName, &username, &avatar,
			&msg.SenderRole, &msg.Content, &msg.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan community message: %w", err)
		}
		if username.Valid {
			msg.SenderUsername = username.String
		}
		if avatar.Valid {
			msg.SenderAvatar = avatar.String
		}
		msg.Reactions = make([]domain.CommunityChatReaction, 0)
		messages = append(messages, msg)
		msgIDs = append(msgIDs, msg.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate community messages: %w", err)
	}

	// Fetch aggregated reactions if there are messages
	if len(msgIDs) > 0 {
		reactionQuery := `
			SELECT message_id, emoji, COUNT(*), array_agg(user_id::text)
			FROM community_message_reactions
			WHERE message_id = ANY($1::uuid[])
			GROUP BY message_id, emoji
			ORDER BY COUNT(*) DESC`
		rRows, err := r.db.QueryContext(ctx, reactionQuery, pq.Array(msgIDs))
		if err == nil {
			defer rRows.Close()
			reactionMap := make(map[string][]domain.CommunityChatReaction)
			for rRows.Next() {
				var messageID, emoji string
				var count int
				var users pq.StringArray
				if err := rRows.Scan(&messageID, &emoji, &count, &users); err == nil {
					reactionMap[messageID] = append(reactionMap[messageID], domain.CommunityChatReaction{
						Emoji: emoji,
						Count: count,
						Users: []string(users),
					})
				}
			}
			for i := range messages {
				if rList, ok := reactionMap[messages[i].ID]; ok {
					messages[i].Reactions = rList
				}
			}
		}
	}

	// Chronological order: reverse descending page to ascending [oldest ... newest]
	// unless querying a single message via id:
	if !strings.HasPrefix(before, "id:") {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}

	return messages, nil
}

func (r *communityRepository) SendCommunityMessage(ctx context.Context, communityID, senderID, content string) (*domain.CommunityChatMessage, error) {
	var msgID string
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO community_messages (community_id, sender_id, content, timestamp, created_at)
		VALUES ($1::uuid, $2::uuid, $3, NOW(), NOW())
		RETURNING id::text`, communityID, senderID, content).Scan(&msgID)
	if err != nil {
		return nil, fmt.Errorf("insert community message: %w", err)
	}

	msgs, err := r.GetCommunityMessages(ctx, communityID, 1, "id:"+msgID)
	if err != nil || len(msgs) == 0 {
		return nil, fmt.Errorf("load created community message: %w", err)
	}

	// Notify PostgreSQL channel for real-time WebSocket distribution
	_, _ = r.db.ExecContext(ctx, `SELECT pg_notify('root_community_events', jsonb_build_object('type', 'community.message.created', 'community_id', $1::text, 'message_id', $2::text)::text)`, communityID, msgID)

	return &msgs[0], nil
}

func (r *communityRepository) ToggleMessageReaction(ctx context.Context, communityID, messageID, userID, emoji string) ([]domain.CommunityChatReaction, error) {
	// 1. Verify message exists and was sent by RRPP or ADMIN
	var senderRole string
	err := r.db.QueryRowContext(ctx, `
		SELECT CASE
			WHEN UPPER(u.role) = 'RRPP' OR c.pr_owner_id = m.sender_id OR cmgr.user_id IS NOT NULL THEN 'RRPP'
			WHEN UPPER(u.role) = 'ADMIN' THEN 'ADMIN'
			ELSE 'MEMBER'
		END AS sender_role
		FROM community_messages m
		JOIN communities c ON c.id = m.community_id
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN community_managers cmgr ON cmgr.community_id = m.community_id AND cmgr.user_id = u.id
		WHERE m.id = $1::uuid AND m.community_id = $2::uuid`, messageID, communityID).Scan(&senderRole)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrCommunityTargetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("verify reaction message: %w", err)
	}

	if senderRole != "RRPP" && senderRole != "ADMIN" {
		return nil, domain.ErrCommunityReactionNotAllowed
	}

	// 2. Toggle: delete if exists, else insert
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM community_message_reactions
		WHERE message_id = $1::uuid AND user_id = $2::uuid AND emoji = $3`, messageID, userID, emoji)
	if err != nil {
		return nil, fmt.Errorf("delete message reaction: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		_, err = r.db.ExecContext(ctx, `
			INSERT INTO community_message_reactions (message_id, user_id, emoji, created_at)
			VALUES ($1::uuid, $2::uuid, $3, NOW())
			ON CONFLICT (message_id, user_id, emoji) DO NOTHING`, messageID, userID, emoji)
		if err != nil {
			return nil, fmt.Errorf("insert message reaction: %w", err)
		}
	}

	// 3. Return updated reactions for the message
	rRows, err := r.db.QueryContext(ctx, `
		SELECT emoji, COUNT(*), array_agg(user_id::text)
		FROM community_message_reactions
		WHERE message_id = $1::uuid
		GROUP BY emoji
		ORDER BY COUNT(*) DESC, emoji ASC`, messageID)
	if err != nil {
		return nil, fmt.Errorf("fetch updated reactions: %w", err)
	}
	defer rRows.Close()

	reactions := make([]domain.CommunityChatReaction, 0)
	for rRows.Next() {
		var em string
		var count int
		var users pq.StringArray
		if err := rRows.Scan(&em, &count, &users); err != nil {
			return nil, fmt.Errorf("scan reaction: %w", err)
		}
		reactions = append(reactions, domain.CommunityChatReaction{
			Emoji: em,
			Count: count,
			Users: []string(users),
		})
	}

	// 4. Notify real-time channel
	_, _ = r.db.ExecContext(ctx, `SELECT pg_notify('root_community_events', jsonb_build_object('type', 'community.reaction.updated', 'community_id', $1::text, 'message_id', $2::text)::text)`, communityID, messageID)

	return reactions, nil
}

func (r *communityRepository) GetCommunityMembers(ctx context.Context, communityID string, limit, offset int, query string) ([]domain.CommunityMember, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	query = strings.TrimSpace(query)

	var countQuery string
	var listQuery string
	var countArgs []interface{}
	var listArgs []interface{}

	if query != "" {
		searchPattern := "%" + query + "%"
		countQuery = `
			SELECT COUNT(*)
			FROM community_members cm
			JOIN users u ON u.id = cm.user_id
			WHERE cm.community_id = $1::uuid
				AND (u.name ILIKE $2 OR u.username ILIKE $2)`
		countArgs = []interface{}{communityID, searchPattern}

		listQuery = `
			SELECT u.id, u.name, u.username, COALESCE(u.avatar_url, ''),
				CASE
					WHEN UPPER(u.role) = 'RRPP' OR c.pr_owner_id = u.id OR cmgr.user_id IS NOT NULL THEN 'RRPP'
					WHEN UPPER(u.role) = 'ADMIN' THEN 'ADMIN'
					ELSE 'MEMBER'
				END AS role,
				cm.joined_at,
				COALESCE(u.is_kyc_verified, FALSE)
			FROM community_members cm
			JOIN communities c ON c.id = cm.community_id
			JOIN users u ON u.id = cm.user_id
			LEFT JOIN community_managers cmgr ON cmgr.community_id = cm.community_id AND cmgr.user_id = u.id
			WHERE cm.community_id = $1::uuid
				AND (u.name ILIKE $2 OR u.username ILIKE $2)
			ORDER BY
				CASE WHEN c.pr_owner_id = u.id THEN 0 ELSE 1 END,
				cm.joined_at ASC,
				u.id ASC
			LIMIT $3 OFFSET $4`
		listArgs = []interface{}{communityID, searchPattern, limit, offset}
	} else {
		countQuery = `
			SELECT COUNT(*)
			FROM community_members cm
			WHERE cm.community_id = $1::uuid`
		countArgs = []interface{}{communityID}

		listQuery = `
			SELECT u.id, u.name, u.username, COALESCE(u.avatar_url, ''),
				CASE
					WHEN UPPER(u.role) = 'RRPP' OR c.pr_owner_id = u.id OR cmgr.user_id IS NOT NULL THEN 'RRPP'
					WHEN UPPER(u.role) = 'ADMIN' THEN 'ADMIN'
					ELSE 'MEMBER'
				END AS role,
				cm.joined_at,
				COALESCE(u.is_kyc_verified, FALSE)
			FROM community_members cm
			JOIN communities c ON c.id = cm.community_id
			JOIN users u ON u.id = cm.user_id
			LEFT JOIN community_managers cmgr ON cmgr.community_id = cm.community_id AND cmgr.user_id = u.id
			WHERE cm.community_id = $1::uuid
			ORDER BY
				CASE WHEN c.pr_owner_id = u.id THEN 0 ELSE 1 END,
				cm.joined_at ASC,
				u.id ASC
			LIMIT $2 OFFSET $3`
		listArgs = []interface{}{communityID, limit, offset}
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count community members: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query community members: %w", err)
	}
	defer rows.Close()

	members := make([]domain.CommunityMember, 0)
	for rows.Next() {
		var m domain.CommunityMember
		var username, avatar sql.NullString
		if err := rows.Scan(&m.ID, &m.Name, &username, &avatar, &m.Role, &m.JoinedAt, &m.IsKYCVerified); err != nil {
			return nil, 0, fmt.Errorf("scan community member: %w", err)
		}
		if username.Valid {
			m.Username = username.String
		}
		if avatar.Valid {
			m.AvatarURL = avatar.String
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate community members: %w", err)
	}

	return members, total, nil
}

func (r *communityRepository) KickCommunityMember(ctx context.Context, communityID, targetUserID string) (int, error) {
	// Check if target is owner
	var ownerID sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT pr_owner_id::text FROM communities WHERE id = $1::uuid`, communityID).Scan(&ownerID)
	if err != nil {
		return 0, fmt.Errorf("check community owner: %w", err)
	}
	if ownerID.Valid && ownerID.String == targetUserID {
		return 0, domain.ErrCommunityCannotKickOwner
	}

	res, err := r.db.ExecContext(ctx, `DELETE FROM community_members WHERE community_id = $1::uuid AND user_id = $2::uuid`, communityID, targetUserID)
	if err != nil {
		return 0, fmt.Errorf("kick community member: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return 0, domain.ErrCommunityTargetNotFound
	}

	var remainingCount int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM community_members WHERE community_id = $1::uuid`, communityID).Scan(&remainingCount); err != nil {
		return 0, fmt.Errorf("count remaining members: %w", err)
	}

	return remainingCount, nil
}

func (r *communityRepository) GetPromotedEvents(ctx context.Context, communityID string) ([]domain.Event, error) {
	query := `
		SELECT e.id, e.title, e.producer_id, e.date, e.end_date, e.location, e.latitude, e.longitude,
			COALESCE(e.cinematic_banner_url, ''), COALESCE(e.description, ''),
			COALESCE(e.lineup, '{}'), e.genre, e.price,
			COALESCE(e.is_featured, false), COALESCE(e.created_at, NOW()),
			COALESCE(counts.going_count, 0), COALESCE(counts.not_going_count, 0), NULL AS status,
			e.ticket_tiers, e.ticket_source, e.ticket_url, e.ticket_info_fetched_at
		FROM community_promoted_events cpe
		JOIN events e ON e.id = cpe.event_id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE status = 'going') AS going_count,
				COUNT(*) FILTER (WHERE status = 'not_going') AS not_going_count
			FROM event_rsvps WHERE event_id = e.id
		) counts ON TRUE
		WHERE cpe.community_id = $1::uuid
		ORDER BY cpe.display_order ASC, cpe.added_at DESC`

	rows, err := r.db.QueryContext(ctx, query, communityID)
	if err != nil {
		return nil, fmt.Errorf("query promoted events: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("scan promoted events: %w", err)
	}
	return events, nil
}

func (r *communityRepository) UpdatePromotedEvents(ctx context.Context, communityID string, eventIDs []string) ([]domain.Event, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update promoted events transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM community_promoted_events WHERE community_id = $1::uuid`, communityID); err != nil {
		return nil, fmt.Errorf("clear promoted events: %w", err)
	}

	for i, eventID := range eventIDs {
		if strings.TrimSpace(eventID) == "" {
			continue
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO community_promoted_events (community_id, event_id, display_order, added_at)
			VALUES ($1::uuid, $2::uuid, $3, NOW())
			ON CONFLICT (community_id, event_id) DO NOTHING`, communityID, eventID, i)
		if err != nil {
			return nil, fmt.Errorf("insert promoted event %s: %w", eventID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update promoted events: %w", err)
	}

	return r.GetPromotedEvents(ctx, communityID)
}

func (r *communityRepository) GetActiveCommunityMemberIDs(ctx context.Context, communityID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id::text FROM community_members WHERE community_id = $1::uuid`, communityID)
	if err != nil {
		return nil, fmt.Errorf("query active community member ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan active community member id: %w", err)
		}
		ids = append(ids, uid)
	}
	return ids, rows.Err()
}
