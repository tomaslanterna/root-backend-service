package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type postRepository struct {
	db *sql.DB
}

func NewPostRepository(db *sql.DB) ports.PostRepository {
	return &postRepository{db: db}
}

func (r *postRepository) GetGlobalPosts(ctx context.Context, limit, offset int) ([]domain.Post, error) {
	query := `
		SELECT 
			p.id, p.author_id, p.event_id, p.community_id, p.title, p.content, p.long_content, 
			p.header_image_url, p.timestamp, p.is_featured,
			COALESCE(u.name, ''), COALESCE(u.avatar_url, ''), COALESCE(u.is_kyc_verified, false)
		FROM posts p
		LEFT JOIN users u ON p.author_id = u.id
		WHERE p.community_id IS NULL
		ORDER BY p.timestamp DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []domain.Post
	for rows.Next() {
		var p domain.Post
		err := rows.Scan(
			&p.ID, &p.AuthorID, &p.EventID, &p.CommunityID, &p.Title,
			&p.Content, &p.LongContent, &p.HeaderImageURL, &p.Timestamp, &p.IsFeatured,
			&p.AuthorName, &p.AuthorAvatar, &p.IsVerified,
		)
		if err != nil {
			return nil, err
		}
		p.Tags = []string{}
		p.LikesCount = 0 // Temporal hardcode
		posts = append(posts, p)
	}

	return posts, nil
}

func (r *postRepository) GetFeaturedPosts(ctx context.Context, limit, offset int) ([]domain.Post, error) {
	query := `
		SELECT 
			p.id, p.author_id, p.event_id, p.community_id, p.title, p.content, p.long_content, 
			p.header_image_url, p.timestamp, p.is_featured,
			COALESCE(u.name, ''), COALESCE(u.avatar_url, ''), COALESCE(u.is_kyc_verified, false)
		FROM posts p
		LEFT JOIN users u ON p.author_id = u.id
		WHERE p.is_featured = TRUE AND p.community_id IS NULL
		ORDER BY p.timestamp DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []domain.Post
	for rows.Next() {
		var p domain.Post
		err := rows.Scan(
			&p.ID, &p.AuthorID, &p.EventID, &p.CommunityID, &p.Title,
			&p.Content, &p.LongContent, &p.HeaderImageURL, &p.Timestamp, &p.IsFeatured,
			&p.AuthorName, &p.AuthorAvatar, &p.IsVerified,
		)
		if err != nil {
			return nil, err
		}
		p.Tags = []string{}
		p.LikesCount = 0
		posts = append(posts, p)
	}

	return posts, nil
}

func (r *postRepository) GetFollowingPosts(ctx context.Context, userID string, limit, offset int) ([]domain.Post, error) {
	query := `
		SELECT 
			p.id, p.author_id, p.event_id, p.community_id, p.title, p.content, p.long_content, 
			p.header_image_url, p.timestamp, p.is_featured,
			COALESCE(u.name, ''), COALESCE(u.avatar_url, ''), COALESCE(u.is_kyc_verified, false)
		FROM posts p
		LEFT JOIN users u ON p.author_id = u.id
		JOIN users viewer ON viewer.id::text = $1
		WHERE p.author_id::text = ANY(COALESCE(viewer.following, '{}'::text[]))
		ORDER BY p.timestamp DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []domain.Post
	for rows.Next() {
		var p domain.Post
		err := rows.Scan(
			&p.ID, &p.AuthorID, &p.EventID, &p.CommunityID, &p.Title,
			&p.Content, &p.LongContent, &p.HeaderImageURL, &p.Timestamp, &p.IsFeatured,
			&p.AuthorName, &p.AuthorAvatar, &p.IsVerified,
		)
		if err != nil {
			return nil, err
		}
		p.Tags = []string{}
		p.LikesCount = 0
		posts = append(posts, p)
	}

	return posts, nil
}

func (r *postRepository) GetPostByID(ctx context.Context, id string) (*domain.Post, error) {
	query := `
		SELECT 
			p.id, p.author_id, p.event_id, p.community_id, p.title, p.content, p.long_content, 
			p.header_image_url, p.timestamp, p.is_featured,
			COALESCE(u.name, ''), COALESCE(u.avatar_url, ''), COALESCE(u.is_kyc_verified, false)
		FROM posts p
		LEFT JOIN users u ON p.author_id = u.id
		WHERE p.id = $1
	`
	var p domain.Post
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.AuthorID, &p.EventID, &p.CommunityID, &p.Title,
		&p.Content, &p.LongContent, &p.HeaderImageURL, &p.Timestamp, &p.IsFeatured,
		&p.AuthorName, &p.AuthorAvatar, &p.IsVerified,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("post not found")
		}
		return nil, err
	}
	p.Tags = []string{}
	p.LikesCount = 0 // Temporal hardcode
	return &p, nil
}

func (r *postRepository) GetCommunityPosts(ctx context.Context, communityID string, limit, offset int) ([]domain.Post, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE community_id::text = $1`, communityID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count community announcements: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.author_id, p.event_id, p.community_id, p.title, p.content,
			p.long_content, p.header_image_url, p.timestamp, p.is_featured, p.is_pinned,
			COALESCE(u.name, ''), COALESCE(u.avatar_url, ''), COALESCE(u.is_kyc_verified, false)
		FROM posts p
		LEFT JOIN users u ON p.author_id = u.id
		WHERE p.community_id::text = $1
		ORDER BY p.is_pinned DESC, p.timestamp DESC, p.id DESC
		LIMIT $2 OFFSET $3`, communityID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list community announcements: %w", err)
	}
	defer rows.Close()

	posts := make([]domain.Post, 0)
	for rows.Next() {
		var post domain.Post
		if err := rows.Scan(
			&post.ID, &post.AuthorID, &post.EventID, &post.CommunityID, &post.Title,
			&post.Content, &post.LongContent, &post.HeaderImageURL, &post.Timestamp,
			&post.IsFeatured, &post.IsPinned, &post.AuthorName, &post.AuthorAvatar, &post.IsVerified,
		); err != nil {
			return nil, 0, fmt.Errorf("scan community announcement: %w", err)
		}
		post.Tags = []string{}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate community announcements: %w", err)
	}
	return posts, total, nil
}

func (r *postRepository) GetPostComments(ctx context.Context, postID string, limit, offset int) ([]domain.EventComment, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE target_type = 'post' AND target_id::text = $1`, postID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.target_id, c.author_id,
			COALESCE(u.name, 'Usuario'), COALESCE(u.username, 'usuario'),
			u.avatar_url, c.content, COALESCE(c.timestamp, NOW())
		FROM comments c LEFT JOIN users u ON c.author_id = u.id
		WHERE c.target_type = 'post' AND c.target_id::text = $1
		ORDER BY c.timestamp DESC, c.id DESC LIMIT $2 OFFSET $3`, postID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	comments := make([]domain.EventComment, 0)
	for rows.Next() {
		var comment domain.EventComment
		var avatarURL, targetID, authorID sql.NullString
		if err := rows.Scan(
			&comment.ID, &targetID, &authorID, &comment.AuthorName,
			&comment.AuthorUsername, &avatarURL, &comment.Content, &comment.Timestamp,
		); err != nil {
			return nil, 0, err
		}
		if targetID.Valid {
			comment.TargetID = targetID.String
		}
		if authorID.Valid {
			comment.AuthorID = authorID.String
		}
		if avatarURL.Valid {
			comment.AuthorAvatar = &avatarURL.String
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return comments, total, nil
}

func (r *postRepository) CreatePostComment(ctx context.Context, postID, authorID, content string) (*domain.EventComment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM posts WHERE id::text = $1)`, postID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, sql.ErrNoRows
	}

	comment := &domain.EventComment{TargetID: postID, AuthorID: authorID, Content: content}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO comments (id, target_type, target_id, author_id, content, timestamp)
		SELECT gen_random_uuid(), 'post', $1::uuid, u.id, $3, NOW()
		FROM posts p, users u WHERE p.id = $1::uuid AND u.id = $2::uuid
		RETURNING id, timestamp`, postID, authorID, content).Scan(&comment.ID, &comment.Timestamp); err != nil {
		return nil, err
	}

	var avatarURL sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT name, username, avatar_url FROM users WHERE id::text = $1`, authorID).Scan(
		&comment.AuthorName, &comment.AuthorUsername, &avatarURL,
	); err != nil {
		return nil, err
	}
	if avatarURL.Valid {
		comment.AuthorAvatar = &avatarURL.String
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return comment, nil
}

func (r *postRepository) CreatePost(ctx context.Context, post *domain.Post) error {
	query := `
		INSERT INTO posts (id, author_id, community_id, event_id, title, content, long_content, header_image_url, timestamp, is_featured)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, NOW(), false)
		RETURNING id, timestamp
	`
	var communityID, eventID sql.NullString
	if post.CommunityID != nil && *post.CommunityID != "" {
		communityID = sql.NullString{String: *post.CommunityID, Valid: true}
	}
	if post.EventID != nil && *post.EventID != "" {
		eventID = sql.NullString{String: *post.EventID, Valid: true}
	}

	err := r.db.QueryRowContext(ctx, query,
		post.AuthorID,
		communityID,
		eventID,
		post.Title,
		post.Content,
		post.LongContent,
		post.HeaderImageURL,
	).Scan(&post.ID, &post.Timestamp)

	return err
}
