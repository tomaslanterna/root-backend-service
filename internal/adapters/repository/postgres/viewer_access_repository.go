package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// ViewerAccessRepository lee de `users` solo lo que hace falta para autorizar (KYC, edad, rol, cuenta disponible).
type ViewerAccessRepository struct{ db *sql.DB }

var _ ports.ViewerAccessRepository = (*ViewerAccessRepository)(nil)

func NewViewerAccessRepository(db *sql.DB) *ViewerAccessRepository {
	return &ViewerAccessRepository{db: db}
}

func (r *ViewerAccessRepository) ViewerAccess(ctx context.Context, userID string) (domain.ViewerAccess, error) {
	access := domain.ViewerAccess{UserID: userID}
	if nullUUID(userID) == nil {
		return access, domain.ErrUserNotFound
	}
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(is_kyc_verified, FALSE), COALESCE(dob, ''), COALESCE(role, ''),
 (deleted_at IS NOT NULL OR suspended_at IS NOT NULL)
 FROM users WHERE id = $1::uuid`, userID).Scan(&access.IsKycVerified, &access.Dob, &access.Role, &access.Unavailable)
	if errors.Is(err, sql.ErrNoRows) {
		return access, domain.ErrUserNotFound
	}
	if err != nil {
		return access, fmt.Errorf("reading viewer access: %w", err)
	}
	return access, nil
}
