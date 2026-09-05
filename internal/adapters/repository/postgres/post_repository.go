package postgres

import (
	"context"
	"database/sql"
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
		INNER JOIN user_followers uf ON p.author_id = uf.followed_id
		LEFT JOIN users u ON p.author_id = u.id
		WHERE uf.follower_id = $1
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
