package postgres

import (
	"context"
	"database/sql"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type transferRepository struct {
	db *sql.DB
}

func NewTransferRepository(db *sql.DB) ports.TransferRepository {
	return &transferRepository{db: db}
}

func (r *transferRepository) CreateTransfer(ctx context.Context, transfer *domain.Transfer) error {
	query := `
		INSERT INTO transfers (id, event_id, seller_id, buyer_id, chat_id, ticket_file_url, status, price_agreed, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		transfer.ID, transfer.EventID, transfer.SellerID, transfer.BuyerID,
		transfer.ChatID, transfer.TicketFileURL, transfer.Status, transfer.PriceAgreed,
		transfer.CreatedAt, transfer.UpdatedAt,
	)
	return err
}

func (r *transferRepository) GetTransferByID(ctx context.Context, id string) (*domain.Transfer, error) {
	query := `
		SELECT id, event_id, seller_id, buyer_id, chat_id, ticket_file_url, status, price_agreed, created_at, updated_at
		FROM transfers
		WHERE id = $1
	`
	var t domain.Transfer
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&t.ID, &t.EventID, &t.SellerID, &t.BuyerID, &t.ChatID,
		&t.TicketFileURL, &t.Status, &t.PriceAgreed, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("transfer not found")
		}
		return nil, err
	}
	return &t, nil
}

func (r *transferRepository) UpdateStatus(ctx context.Context, transferID string, status domain.TransferStatus, ticketURL *string) error {
	query := `
		UPDATE transfers
		SET status = $1, ticket_file_url = COALESCE($2, ticket_file_url), updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, status, ticketURL, transferID)
	return err
}

func (r *transferRepository) UpdateStartDeal(ctx context.Context, transferID, buyerID, chatID string) error {
	query := `
		UPDATE transfers
		SET status = $1, buyer_id = $2, chat_id = $3, updated_at = NOW()
		WHERE id = $4 AND status = $5 AND buyer_id IS NULL
	`
	res, err := r.db.ExecContext(ctx, query, domain.TransferStatusNegotiating, buyerID, chatID, transferID, domain.TransferStatusAvailable)
	if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errors.New("conflict: transfer not available")
	}
	return nil
}

func (r *transferRepository) GetTransfers(ctx context.Context, status *string) ([]domain.Transfer, error) {
	query := `
		SELECT id, event_id, seller_id, buyer_id, chat_id, ticket_file_url, status, price_agreed, created_at, updated_at
		FROM transfers
	`
	var args []interface{}
	if status != nil {
		query += ` WHERE status = $1`
		args = append(args, *status)
	}
	query += ` ORDER BY created_at DESC LIMIT 50`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transfers []domain.Transfer
	for rows.Next() {
		var t domain.Transfer
		err := rows.Scan(
			&t.ID, &t.EventID, &t.SellerID, &t.BuyerID, &t.ChatID,
			&t.TicketFileURL, &t.Status, &t.PriceAgreed, &t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, t)
	}
	return transfers, nil
}

