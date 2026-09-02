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
		SELECT id, author_id, event_id, community_id, title, content, long_content, header_image_url, timestamp, is_featured
		FROM posts 
		WHERE community_id IS NULL
		ORDER BY timestamp DESC
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
		)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}

	return posts, nil
}

func (r *postRepository) GetFeaturedPosts(ctx context.Context, limit, offset int) ([]domain.Post, error) {
	query := `
		SELECT id, author_id, event_id, community_id, title, content, long_content, header_image_url, timestamp, is_featured
		FROM posts 
		WHERE is_featured = TRUE AND community_id IS NULL
		ORDER BY timestamp DESC
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
		)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}

	return posts, nil
}

func (r *postRepository) GetFollowingPosts(ctx context.Context, userID string, limit, offset int) ([]domain.Post, error) {
	query := `
		SELECT p.id, p.author_id, p.event_id, p.community_id, p.title, p.content, p.long_content, p.header_image_url, p.timestamp, p.is_featured
		FROM posts p
		INNER JOIN user_followers uf ON p.author_id = uf.followed_id
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
		)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}

	return posts, nil
}
