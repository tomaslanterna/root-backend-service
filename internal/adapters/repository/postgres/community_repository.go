package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
)

type communityRepository struct {
	db *sql.DB
}

func NewCommunityRepository(db *sql.DB) ports.CommunityRepository {
	return &communityRepository{db: db}
}

func (r *communityRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin community schema transaction: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS unaccent`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS slug TEXT`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS category TEXT`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS zone TEXT`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE communities ALTER COLUMN pr_owner_id DROP NOT NULL`,
		`UPDATE communities SET slug = 'community-' || id::text WHERE slug IS NULL OR BTRIM(slug) = ''`,
		`UPDATE communities SET category = 'general' WHERE category IS NULL OR BTRIM(category) = ''`,
		`ALTER TABLE communities ALTER COLUMN slug SET NOT NULL`,
		`ALTER TABLE communities ALTER COLUMN category SET NOT NULL`,
		`ALTER TABLE communities ALTER COLUMN category SET DEFAULT 'general'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_communities_slug_unique ON communities (LOWER(slug))`,
		`CREATE INDEX IF NOT EXISTS idx_communities_discovery ON communities (is_active, country_id, category, name)`,
		`DELETE FROM community_members a USING community_members b WHERE a.ctid < b.ctid AND a.community_id = b.community_id AND a.user_id = b.user_id`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_community_members_unique ON community_members (community_id, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_community_members_user ON community_members (user_id, joined_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_community_members_community ON community_members (community_id)`,
		`CREATE TABLE IF NOT EXISTS community_managers (
			community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (community_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_community_managers_user ON community_managers (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_posts_community_timestamp ON posts (community_id, timestamp DESC, id) WHERE community_id IS NOT NULL`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize community schema: %w", err)
		}
	}

	seedQuery := `
		INSERT INTO communities (
			id, name, slug, category, zone, pr_owner_id, country_id,
			cover_image_url, description, created_at, is_active
		) VALUES
			(gen_random_uuid(), 'Electrónica', 'electronica', 'electrónica', NULL, NULL, 'UY', '', 'Para quienes viven la electrónica, sus fechas, artistas y novedades.', NOW(), TRUE),
			(gen_random_uuid(), 'Cachengue', 'cachengue', 'cachengue', NULL, NULL, 'UY', '', 'La comunidad para descubrir fiestas, fechas y novedades de cachengue.', NOW(), TRUE),
			(gen_random_uuid(), 'Reggaetón', 'reggaeton', 'reggaetón', NULL, NULL, 'UY', '', 'Fechas, anuncios y novedades para quienes disfrutan del reggaetón.', NOW(), TRUE)
		ON CONFLICT (LOWER(slug)) DO UPDATE SET
			is_active = TRUE,
			zone = CASE WHEN communities.zone = 'Uruguay' THEN NULL ELSE communities.zone END
	`
	if _, err := tx.ExecContext(ctx, seedQuery); err != nil {
		return fmt.Errorf("seed predefined communities: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit community schema transaction: %w", err)
	}
	return nil
}

func communitySelect() string {
	return `
		SELECT c.id, c.name, c.slug, c.category, COALESCE(c.zone, ''), c.pr_owner_id,
			c.country_id, COALESCE(c.cover_image_url, ''), COALESCE(c.description, ''),
			c.created_at, COALESCE(mc.members_count, 0),
			EXISTS (
				SELECT 1 FROM community_members viewer_membership
				WHERE viewer_membership.community_id = c.id AND viewer_membership.user_id::text = $1
			),
			COALESCE((
				EXISTS (SELECT 1 FROM users viewer WHERE viewer.id::text = $1 AND UPPER(viewer.role) = 'ADMIN')
				OR EXISTS (
					SELECT 1 FROM users owner_user
					WHERE owner_user.id = c.pr_owner_id AND owner_user.id::text = $1
						AND UPPER(owner_user.role) = 'RRPP'
				)
				OR EXISTS (
					SELECT 1 FROM community_managers manager
					JOIN users manager_user ON manager_user.id = manager.user_id
					WHERE manager.community_id = c.id AND manager.user_id::text = $1
						AND UPPER(manager_user.role) = 'RRPP'
				)
			), FALSE),
			c.is_active
		FROM communities c
		LEFT JOIN (
			SELECT community_id, COUNT(*)::int AS members_count
			FROM community_members GROUP BY community_id
		) mc ON mc.community_id = c.id
	`
}

func scanCommunity(scanner interface{ Scan(...any) error }) (*domain.Community, error) {
	var community domain.Community
	if err := scanner.Scan(
		&community.ID, &community.Name, &community.Slug, &community.Category, &community.Zone,
		&community.PROwnerID, &community.CountryID, &community.CoverImageURL, &community.Description,
		&community.CreatedAt, &community.MembersCount, &community.IsMember, &community.CanPublish,
		&community.IsActive,
	); err != nil {
		return nil, err
	}
	return &community, nil
}

func (r *communityRepository) GetCommunities(ctx context.Context, filter domain.CommunityFilter, currentUserID string) ([]domain.Community, int, error) {
	countWhere, countArgs := buildCommunityWhere(filter, 1)
	countQuery := "SELECT COUNT(*) FROM communities c WHERE " + countWhere
	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count communities: %w", err)
	}

	where, filterArgs := buildCommunityWhere(filter, 2)
	args := append([]any{currentUserID}, filterArgs...)
	args = append(args, filter.Limit, filter.Offset)
	query := communitySelect() + " WHERE " + where + fmt.Sprintf(" ORDER BY c.name ASC, c.id ASC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list communities: %w", err)
	}
	defer rows.Close()

	communities := make([]domain.Community, 0)
	for rows.Next() {
		community, err := scanCommunity(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan community: %w", err)
		}
		communities = append(communities, *community)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate communities: %w", err)
	}
	return communities, total, nil
}

func buildCommunityWhere(filter domain.CommunityFilter, firstPlaceholder int) (string, []any) {
	conditions := []string{"c.is_active = TRUE"}
	args := make([]any, 0, 4)
	addCondition := func(format, value string) {
		placeholder := firstPlaceholder + len(args)
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(format, placeholder))
	}
	if filter.Country != "" {
		addCondition("UPPER(c.country_id) = UPPER($%d)", filter.Country)
	}
	if filter.Category != "" {
		addCondition("unaccent(LOWER(c.category)) = unaccent(LOWER($%d))", filter.Category)
	}
	if filter.Department != "" {
		addCondition("unaccent(LOWER(COALESCE(c.zone, ''))) = unaccent(LOWER($%d))", filter.Department)
	}
	if filter.Query != "" {
		placeholder := firstPlaceholder + len(args)
		args = append(args, filter.Query)
		conditions = append(conditions, fmt.Sprintf("(unaccent(LOWER(c.name)) LIKE '%%' || unaccent(LOWER($%d)) || '%%' OR unaccent(LOWER(COALESCE(c.description, ''))) LIKE '%%' || unaccent(LOWER($%d)) || '%%')", placeholder, placeholder))
	}
	return strings.Join(conditions, " AND "), args
}

func (r *communityRepository) GetCommunityByIDOrSlug(ctx context.Context, identifier string, currentUserID string) (*domain.Community, error) {
	query := communitySelect() + " WHERE c.is_active = TRUE AND (c.id::text = $2 OR LOWER(c.slug) = LOWER($2))"
	community, err := scanCommunity(r.db.QueryRowContext(ctx, query, currentUserID, identifier))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrCommunityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get community: %w", err)
	}
	return community, nil
}

func (r *communityRepository) JoinCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return r.changeMembership(ctx, communityID, userID, true)
}

func (r *communityRepository) LeaveCommunity(ctx context.Context, communityID, userID string) (int, error) {
	return r.changeMembership(ctx, communityID, userID, false)
}

func (r *communityRepository) changeMembership(ctx context.Context, communityID, userID string, join bool) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin membership transaction: %w", err)
	}
	defer tx.Rollback()

	var resolvedID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM communities WHERE is_active = TRUE AND (id::text = $1 OR LOWER(slug) = LOWER($1))`, communityID).Scan(&resolvedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.ErrCommunityNotFound
		}
		return 0, fmt.Errorf("resolve community membership: %w", err)
	}

	if join {
		if _, err := tx.ExecContext(ctx, `INSERT INTO community_members (community_id, user_id, joined_at) VALUES ($1::uuid, $2::uuid, NOW()) ON CONFLICT (community_id, user_id) DO NOTHING`, resolvedID, userID); err != nil {
			return 0, fmt.Errorf("join community: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM community_members WHERE community_id = $1::uuid AND user_id = $2::uuid`, resolvedID, userID); err != nil {
		return 0, fmt.Errorf("leave community: %w", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM community_members WHERE community_id = $1::uuid`, resolvedID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count community members: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit membership transaction: %w", err)
	}
	return count, nil
}

func (r *communityRepository) GetUserCommunities(ctx context.Context, username, currentUserID string) ([]domain.Community, error) {
	query := communitySelect() + `
		JOIN community_members profile_membership ON profile_membership.community_id = c.id
		JOIN users profile_user ON profile_user.id = profile_membership.user_id
		WHERE c.is_active = TRUE AND profile_user.username = $2
		ORDER BY c.name ASC, c.id ASC`
	rows, err := r.db.QueryContext(ctx, query, currentUserID, username)
	if err != nil {
		return nil, fmt.Errorf("get user communities: %w", err)
	}
	return collectCommunities(rows)
}

func (r *communityRepository) GetCurrentUserCommunities(ctx context.Context, userID string) ([]domain.Community, error) {
	query := communitySelect() + `
		JOIN community_members my_membership ON my_membership.community_id = c.id
		WHERE c.is_active = TRUE AND my_membership.user_id::text = $1
		ORDER BY my_membership.joined_at DESC, c.id ASC`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get current user communities: %w", err)
	}
	return collectCommunities(rows)
}

func collectCommunities(rows *sql.Rows) ([]domain.Community, error) {
	defer rows.Close()
	communities := make([]domain.Community, 0)
	for rows.Next() {
		community, err := scanCommunity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan community: %w", err)
		}
		communities = append(communities, *community)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate communities: %w", err)
	}
	return communities, nil
}

func (r *communityRepository) CanPublish(ctx context.Context, communityID, userID string) (bool, bool, error) {
	var exists, allowed bool
	err := r.db.QueryRowContext(ctx, `
		SELECT TRUE, COALESCE((
			EXISTS (SELECT 1 FROM users u WHERE u.id::text = $2 AND UPPER(u.role) = 'ADMIN')
			OR EXISTS (
				SELECT 1 FROM users owner_user
				WHERE owner_user.id = c.pr_owner_id AND owner_user.id::text = $2
					AND UPPER(owner_user.role) = 'RRPP'
			)
			OR EXISTS (
				SELECT 1 FROM community_managers cm
				JOIN users manager_user ON manager_user.id = cm.user_id
				WHERE cm.community_id = c.id AND cm.user_id::text = $2
					AND UPPER(manager_user.role) = 'RRPP'
			)
		), FALSE)
		FROM communities c
		WHERE c.is_active = TRUE AND (c.id::text = $1 OR LOWER(c.slug) = LOWER($1))`, communityID, userID).Scan(&exists, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("check community publish permission: %w", err)
	}
	return exists, allowed, nil
}
