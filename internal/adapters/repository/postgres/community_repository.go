package postgres

import (
	"context"
	"database/sql"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type communityRepository struct {
	db *sql.DB
}

func NewCommunityRepository(db *sql.DB) ports.CommunityRepository {
	return &communityRepository{db: db}
}

func (r *communityRepository) GetCommunitiesByCountry(ctx context.Context, countryID string, currentUserID string, limit, offset int) ([]domain.Community, error) {
	query := `
		SELECT c.id, c.name, c.pr_owner_id, c.country_id, c.cover_image_url, c.description, c.created_at,
		       (SELECT COUNT(*) FROM community_members cm WHERE cm.community_id = c.id) as members_count,
		       EXISTS(SELECT 1 FROM community_members cm WHERE cm.community_id = c.id AND cm.user_id::text = $2) as is_member
		FROM communities c
		WHERE c.country_id = $1
		ORDER BY c.created_at DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.db.QueryContext(ctx, query, countryID, currentUserID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var communities []domain.Community
	for rows.Next() {
		var c domain.Community
		err := rows.Scan(
			&c.ID, &c.Name, &c.PROwnerID, &c.CountryID, &c.CoverImageURL, &c.Description, &c.CreatedAt, &c.MembersCount, &c.IsMember,
		)
		if err != nil {
			return nil, err
		}
		communities = append(communities, c)
	}
	return communities, nil
}

func (r *communityRepository) GetCommunityByID(ctx context.Context, id string, currentUserID string) (*domain.Community, error) {
	query := `
		SELECT c.id, c.name, c.pr_owner_id, c.country_id, c.cover_image_url, c.description, c.created_at,
		       (SELECT COUNT(*) FROM community_members cm WHERE cm.community_id = c.id) as members_count,
		       EXISTS(SELECT 1 FROM community_members cm WHERE cm.community_id = c.id AND cm.user_id::text = $2) as is_member
		FROM communities c
		WHERE c.id = $1
	`
	var c domain.Community
	err := r.db.QueryRowContext(ctx, query, id, currentUserID).Scan(
		&c.ID, &c.Name, &c.PROwnerID, &c.CountryID, &c.CoverImageURL, &c.Description, &c.CreatedAt, &c.MembersCount, &c.IsMember,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *communityRepository) ToggleJoinCommunity(ctx context.Context, communityID, userID string) (bool, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM community_members WHERE community_id = $1 AND user_id = $2)", communityID, userID).Scan(&exists)
	if err != nil {
		return false, 0, err
	}

	isMember := false
	if exists {
		_, err = tx.ExecContext(ctx, "DELETE FROM community_members WHERE community_id = $1 AND user_id = $2", communityID, userID)
		if err != nil {
			return false, 0, err
		}
		isMember = false
	} else {
		_, err = tx.ExecContext(ctx, "INSERT INTO community_members (community_id, user_id, joined_at) VALUES ($1, $2, NOW())", communityID, userID)
		if err != nil {
			return false, 0, err
		}
		isMember = true
	}

	var count int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM community_members WHERE community_id = $1", communityID).Scan(&count)
	if err != nil {
		return false, 0, err
	}

	if err = tx.Commit(); err != nil {
		return false, 0, err
	}

	return isMember, count, nil
}

func (r *communityRepository) GetUserCommunities(ctx context.Context, username string) ([]domain.Community, error) {
	query := `
		SELECT c.id, c.name, c.pr_owner_id, c.country_id, c.cover_image_url, c.description, c.created_at,
		       (SELECT COUNT(*) FROM community_members cm_count WHERE cm_count.community_id = c.id) as members_count,
		       true as is_member
		FROM communities c
		JOIN community_members cm ON c.id = cm.community_id
		JOIN users u ON u.id = cm.user_id
		WHERE u.username = $1
		ORDER BY c.name ASC
	`
	rows, err := r.db.QueryContext(ctx, query, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var communities []domain.Community
	for rows.Next() {
		var c domain.Community
		if err := rows.Scan(
			&c.ID, &c.Name, &c.PROwnerID, &c.CountryID, &c.CoverImageURL, &c.Description, &c.CreatedAt, &c.MembersCount, &c.IsMember,
		); err != nil {
			return nil, err
		}
		communities = append(communities, c)
	}

	return communities, nil
}
