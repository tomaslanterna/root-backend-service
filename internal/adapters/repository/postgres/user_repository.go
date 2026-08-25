package postgres

import (
	"context"
	"database/sql"
	"errors"
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
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, created_at, updated_at 
		FROM users 
		WHERE email = $1
	`
	
	user := &domain.User{}
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.CreatedAt, &user.UpdatedAt,
	)
	
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (r *userRepository) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, created_at, updated_at 
		FROM users 
		WHERE username = $1
	`
	
	user := &domain.User{}
	err := r.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	return user, nil
}

func (r *userRepository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	sqlQuery := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, created_at, updated_at
		FROM users
		WHERE id = $1
	`
	err := r.db.QueryRowContext(ctx, sqlQuery, id).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Username,
		&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.CreatedAt, &user.UpdatedAt,
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
		SET username = $1, dob = $2, document_id = $3, country = $4, updated_at = NOW()
		WHERE id = $5
	`
	_, err := r.db.ExecContext(ctx, sqlQuery, user.Username, user.Dob, user.DocumentID, user.Country, user.ID)
	return err
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

func (r *userRepository) SearchUsers(ctx context.Context, query string, currentUserID string) ([]domain.User, error) {
	if currentUserID == "" {
		// Use a dummy UUID to prevent postgres type errors when id != $2
		currentUserID = "00000000-0000-0000-0000-000000000000"
	}

	sqlQuery := `
		SELECT id, email, password_hash, name, username, role, avatar_url, dob, document_id, followers, following, is_kyc_verified, country, created_at, updated_at 
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
			&user.Role, &user.AvatarURL, &user.Dob, &user.DocumentID, pq.Array(&user.Followers), pq.Array(&user.Following), &user.IsKycVerified, &user.Country, &user.CreatedAt, &user.UpdatedAt,
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
