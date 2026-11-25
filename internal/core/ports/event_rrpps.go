package ports

import (
	"context"

	"root-backend-service/internal/core/domain"
)

// EventRRPPRepository persiste la relación evento ↔ RRPP oficial (tabla event_rrpps).
type EventRRPPRepository interface {
	// ListForEvent devuelve los RRPPs del evento ordenados por position y nombre; nunca nil.
	ListForEvent(ctx context.Context, eventID string) ([]domain.EventRRPP, error)
	// Assign crea o actualiza la asignación. Devuelve domain.ErrEventNotFound, domain.ErrUserNotFound o
	// domain.ErrNotRRPP si el evento no existe, el usuario no existe o no tiene rol RRPP.
	Assign(ctx context.Context, eventID, userID string, position int) error
	// Remove quita la asignación; false si no existía.
	Remove(ctx context.Context, eventID, userID string) (bool, error)
}

// EventRRPPService valida la entrada y delega en el repositorio. La asignación no se expone por HTTP:
// solo la usa el comando administrativo cmd/event_rrpps.
type EventRRPPService interface {
	ListForEvent(ctx context.Context, eventID string) ([]domain.EventRRPP, error)
	Assign(ctx context.Context, eventID, userID string, position int) error
	Remove(ctx context.Context, eventID, userID string) (bool, error)
}
