package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"

	"github.com/lib/pq"
)

type userRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) ports.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	query := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, COALESCE(is_private, false), created_at, updated_at 
		FROM users 
		WHERE email = $1
	`
	
	user := &domain.User{}
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.IsPrivate, &user.CreatedAt, &user.UpdatedAt,
	)
	
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (r *userRepository) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, COALESCE(is_private, false), created_at, updated_at 
		FROM users 
		WHERE username = $1
	`
	
	user := &domain.User{}
	err := r.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.IsPrivate, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}

func (r *userRepository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	sqlQuery := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, COALESCE(is_private, false), created_at, updated_at
		FROM users
		WHERE id = $1
	`
	err := r.db.QueryRowContext(ctx, sqlQuery, id).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.IsPrivate, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) CreateUser(ctx context.Context, user *domain.User) error {
	query := `
		INSERT INTO users (id, email, password_hash, name, username, role, avatar_url, dob, document_id, country, followers, following, is_kyc_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`
	
	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Email, user.PasswordHash, user.Name, user.Username,
		user.Role, user.AvatarURL, user.Dob, user.DocumentID, user.Country, pq.Array(user.Followers), pq.Array(user.Following), user.IsKycVerified, user.CreatedAt, user.UpdatedAt,
	)
	
	return err
}

func (r *userRepository) UpdateUser(ctx context.Context, user *domain.User) error {
	sqlQuery := `
		UPDATE users 
		SET username = $1, dob = $2, document_id = $3, country = $4, name = $5, updated_at = NOW()
		WHERE id = $6
	`
	_, err := r.db.ExecContext(ctx, sqlQuery, user.Username, user.Dob, user.DocumentID, user.Country, user.Name, user.ID)
	return err
}

func (r *userRepository) UpdateAvatar(ctx context.Context, userID, avatar string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE users SET avatar_url = $1, updated_at = NOW() WHERE id = $2`, avatar, userID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *userRepository) AddFollower(ctx context.Context, followerID, followedID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update the followed user's followers list
	_, err = tx.ExecContext(ctx, `UPDATE users SET followers = array_append(COALESCE(followers, '{}'::text[]), $1) WHERE id = $2 AND NOT ($1 = ANY(COALESCE(followers, '{}'::text[])))`, followerID, followedID)
	if err != nil {
		return err
	}

	// Update the follower's following list
	_, err = tx.ExecContext(ctx, `UPDATE users SET following = array_append(COALESCE(following, '{}'::text[]), $1) WHERE id = $2 AND NOT ($1 = ANY(COALESCE(following, '{}'::text[])))`, followedID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *userRepository) RemoveFollower(ctx context.Context, followerID, followedID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update the followed user's followers list
	_, err = tx.ExecContext(ctx, `UPDATE users SET followers = array_remove(COALESCE(followers, '{}'::text[]), $1) WHERE id = $2`, followerID, followedID)
	if err != nil {
		return err
	}

	// Update the follower's following list
	_, err = tx.ExecContext(ctx, `UPDATE users SET following = array_remove(COALESCE(following, '{}'::text[]), $1) WHERE id = $2`, followedID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ListFollowers devuelve quienes siguen a userID (users.followers), del más reciente al más viejo.
func (r *userRepository) ListFollowers(ctx context.Context, userID, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	return r.listFollowColumn(ctx, "followers", userID, viewerID, limit, offset)
}

// ListFollowing devuelve a quienes sigue userID (users.following), del más reciente al más viejo.
func (r *userRepository) ListFollowing(ctx context.Context, userID, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	return r.listFollowColumn(ctx, "following", userID, viewerID, limit, offset)
}

// listFollowColumn recorre el array de IDs (followers o following) en orden de agregado y lo cruza con users,
// marcando si quien mira (viewerID, puede ser "") sigue a cada persona. column sale solo de las dos funciones de arriba.
func (r *userRepository) listFollowColumn(ctx context.Context, column, userID, viewerID string, limit, offset int) ([]domain.FollowListUser, int, error) {
	ids := `WITH ids AS (
			SELECT f.id, f.ord
			FROM users t, unnest(COALESCE(t.` + column + `, '{}'::text[])) WITH ORDINALITY AS f(id, ord)
			WHERE t.id::text = $1
		)`

	var total int
	if err := r.db.QueryRowContext(ctx, ids+` SELECT COUNT(*) FROM ids JOIN users u ON u.id::text = ids.id`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting %s: %w", column, err)
	}

	rows, err := r.db.QueryContext(ctx, ids+`
		SELECT u.id, COALESCE(u.name, ''), u.username, u.avatar_url, COALESCE(u.is_kyc_verified, false),
		       COALESCE(u.id::text = ANY(COALESCE(v.following, '{}'::text[])), false)
		FROM ids
		JOIN users u ON u.id::text = ids.id
		LEFT JOIN users v ON v.id::text = $2
		ORDER BY ids.ord DESC
		LIMIT $3 OFFSET $4`, userID, viewerID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("querying %s: %w", column, err)
	}
	defer rows.Close()

	users := make([]domain.FollowListUser, 0)
	for rows.Next() {
		var u domain.FollowListUser
		var avatarURL sql.NullString
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &avatarURL, &u.IsKycVerified, &u.IsFollowing); err != nil {
			return nil, 0, fmt.Errorf("scanning %s: %w", column, err)
		}
		if avatarURL.Valid {
			u.AvatarURL = &avatarURL.String
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating %s: %w", column, err)
	}
	return users, total, nil
}

// GetInterests devuelve los géneros elegidos y si la persona ya respondió la pregunta de intereses.
func (r *userRepository) GetInterests(ctx context.Context, userID string) ([]string, bool, error) {
	var genres []string
	var answered bool
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(favorite_genres, '{}'::text[]), interests_answered_at IS NOT NULL
		FROM users WHERE id::text = $1
	`, userID).Scan(pq.Array(&genres), &answered)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, domain.ErrUserNotFound
		}
		return nil, false, err
	}
	if genres == nil {
		genres = []string{}
	}
	return genres, answered, nil
}

// SetInterests guarda los géneros (puede ser una lista vacía) y marca la pregunta como respondida.
func (r *userRepository) SetInterests(ctx context.Context, userID string, genres []string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE users SET favorite_genres = $2, interests_answered_at = NOW(), updated_at = NOW()
		WHERE id::text = $1
	`, userID, pq.Array(genres))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// SetPrivate cambia el perfil entre público y privado.
func (r *userRepository) SetPrivate(ctx context.Context, userID string, isPrivate bool) error {
	result, err := r.db.ExecContext(ctx, `UPDATE users SET is_private = $2, updated_at = NOW() WHERE id::text = $1`, userID, isPrivate)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// CreateFollowRequest registra que requesterID quiere seguir a targetID (idempotente).
func (r *userRepository) CreateFollowRequest(ctx context.Context, requesterID, targetID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO follow_requests (requester_id, target_id) VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (requester_id, target_id) DO NOTHING
	`, requesterID, targetID)
	return err
}

// DeleteFollowRequest borra la solicitud; devuelve si existía.
func (r *userRepository) DeleteFollowRequest(ctx context.Context, requesterID, targetID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM follow_requests WHERE requester_id::text = $1 AND target_id::text = $2`, requesterID, targetID)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// HasFollowRequest indica si hay una solicitud pendiente de requesterID a targetID.
func (r *userRepository) HasFollowRequest(ctx context.Context, requesterID, targetID string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM follow_requests WHERE requester_id::text = $1 AND target_id::text = $2)
	`, requesterID, targetID).Scan(&exists)
	return exists, err
}

// ListFollowRequests devuelve quienes pidieron seguir a targetID, de la más nueva a la más vieja.
func (r *userRepository) ListFollowRequests(ctx context.Context, targetID string) ([]domain.FollowListUser, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.name, ''), u.username, u.avatar_url, COALESCE(u.is_kyc_verified, false)
		FROM follow_requests fr
		JOIN users u ON u.id = fr.requester_id
		WHERE fr.target_id::text = $1
		ORDER BY fr.created_at DESC
		LIMIT 200
	`, targetID)
	if err != nil {
		return nil, fmt.Errorf("listing follow requests: %w", err)
	}
	defer rows.Close()
	users := make([]domain.FollowListUser, 0)
	for rows.Next() {
		var u domain.FollowListUser
		var avatarURL sql.NullString
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &avatarURL, &u.IsKycVerified); err != nil {
			return nil, fmt.Errorf("scanning follow request: %w", err)
		}
		if avatarURL.Valid {
			u.AvatarURL = &avatarURL.String
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *userRepository) SearchUsers(ctx context.Context, query string, currentUserID string) ([]domain.User, error) {
	if currentUserID == "" {
		// Use a dummy UUID to prevent postgres type errors when id != $2
		currentUserID = "00000000-0000-0000-0000-000000000000"
	}

	sqlQuery := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, COALESCE(is_private, false), created_at, updated_at 
		FROM users 
		WHERE (username ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')
		AND id != $2
		LIMIT 20
	`
	
	rows, err := r.db.QueryContext(ctx, sqlQuery, query, currentUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var user domain.User
		err := rows.Scan(
			&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
			&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.IsPrivate, &user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	return users, nil
}

func (r *userRepository) UpdateUserKycStatus(ctx context.Context, userID string, isVerified bool, country string) error {
	sqlQuery := `
		UPDATE users
		SET is_kyc_verified = $1, country = $2, updated_at = NOW()
		WHERE id = $3
	`
	var c *string
	if country != "" {
		c = &country
	}
	_, err := r.db.ExecContext(ctx, sqlQuery, isVerified, c, userID)
	return err
}
