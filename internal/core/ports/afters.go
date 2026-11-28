package ports

import (
	"context"
	"time"

	"root-backend-service/internal/core/domain"
)

// ViewerAccessRepository lee lo necesario para autorizar (KYC, edad, rol, cuenta disponible).
// domain.ErrUserNotFound si el usuario no existe.
type ViewerAccessRepository interface {
	ViewerAccess(ctx context.Context, userID string) (domain.ViewerAccess, error)
}

// AfterRepository persiste los afters, quiénes van y las denuncias. Cada escritura es una transacción; la identidad
// (hostID/userID/adminID) llega siempre del JWT. Ninguna lectura de listados devuelve la dirección.
type AfterRepository interface {
	InitSchema(ctx context.Context) error
	// EventWindow devuelve el inicio y el fin efectivo del evento; domain.ErrEventNotFound si no existe.
	EventWindow(ctx context.Context, eventID string) (domain.AfterEventWindow, error)
	// Create aplica el tope de afters activos por host (domain.ErrTooManyActiveAfters) y devuelve el id.
	Create(ctx context.Context, hostID string, fields domain.AfterFields, now time.Time) (string, error)
	// Current devuelve el after para editarlo; domain.ErrAfterNotFound si no existe o fue eliminado.
	Current(ctx context.Context, afterID string, now time.Time) (domain.AfterCurrent, error)
	// Update guarda los campos (y el aviso, notificando a aceptados y a quienes van). Solo el host.
	Update(ctx context.Context, afterID, hostID string, fields domain.AfterFields, notice domain.NoticeChange) error
	Remove(ctx context.Context, afterID, hostID string) error
	// Detail devuelve el after con los datos privados sin aplicar; domain.ErrAfterNotFound si no lo puede ver.
	Detail(ctx context.Context, afterID, viewerID string) (*domain.AfterDetailRow, error)
	ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.AfterPage, error)
	List(ctx context.Context, filter domain.AfterFilter, viewerID string) (domain.AfterPage, error)
	// Join deja al usuario en requested (approval) o going (free/ticket) y devuelve el estado.
	Join(ctx context.Context, afterID, userID string, message *string) (string, error)
	Leave(ctx context.Context, afterID, userID string) error
	ListRequests(ctx context.Context, afterID, hostID, status string) ([]domain.AfterRequest, error)
	Decide(ctx context.Context, afterID, hostID, userID string, accept bool) error
	// Report guarda la denuncia (una por usuario) y oculta el after al llegar a domain.AfterReportsToHide.
	Report(ctx context.Context, afterID, userID, reason string) error
	AdminList(ctx context.Context, status string, limit, offset int) ([]domain.AdminAfter, error)
	AdminSetStatus(ctx context.Context, afterID, adminID, status string) error
}

// AfterService valida la entrada, aplica las reglas de acceso (KYC, edad, host) y delega en el repositorio.
type AfterService interface {
	ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.AfterPage, error)
	List(ctx context.Context, filter domain.AfterFilter, viewerID string) (domain.AfterPage, error)
	Detail(ctx context.Context, afterID, viewerID string) (*domain.AfterDetail, error)
	Create(ctx context.Context, hostID string, fields domain.AfterFields) (*domain.AfterDetail, error)
	Update(ctx context.Context, afterID, hostID string, patch domain.AfterPatch) (*domain.AfterDetail, error)
	Remove(ctx context.Context, afterID, hostID string) error
	Join(ctx context.Context, afterID, userID, message string) (*domain.AfterDetail, error)
	Leave(ctx context.Context, afterID, userID string) error
	ListRequests(ctx context.Context, afterID, hostID, status string) ([]domain.AfterRequest, error)
	Decide(ctx context.Context, afterID, hostID, userID string, accept bool) error
	Report(ctx context.Context, afterID, userID, reason string) error
	AdminList(ctx context.Context, adminID, status string, limit, offset int) ([]domain.AdminAfter, error)
	AdminSetStatus(ctx context.Context, adminID, afterID, status string) error
}

// EventSubmissionRepository persiste los envíos de "Mandanos tu evento".
type EventSubmissionRepository interface {
	InitSchema(ctx context.Context) error
	// Create aplica el tope de pendientes por usuario (domain.ErrTooManyPendingSubmissions).
	Create(ctx context.Context, userID string, input domain.EventSubmissionInput) (domain.EventSubmission, error)
	ListMine(ctx context.Context, userID string) ([]domain.EventSubmission, error)
	AdminList(ctx context.Context, status string, limit, offset int) ([]domain.AdminEventSubmission, error)
	// Publish crea el evento con los datos del envío y devuelve su id.
	Publish(ctx context.Context, submissionID, adminID string) (string, error)
	Reject(ctx context.Context, submissionID, adminID, reason string) error
	// Similar busca eventos parecidos por nombre (y fecha, si viene) para el "¿Es este?".
	Similar(ctx context.Context, name string, date *time.Time, limit int) ([]domain.Event, error)
}

type EventSubmissionService interface {
	Create(ctx context.Context, userID string, input domain.EventSubmissionInput) (domain.EventSubmission, error)
	ListMine(ctx context.Context, userID string) ([]domain.EventSubmission, error)
	AdminList(ctx context.Context, adminID, status string, limit, offset int) ([]domain.AdminEventSubmission, error)
	Publish(ctx context.Context, adminID, submissionID string) (string, error)
	Reject(ctx context.Context, adminID, submissionID, reason string) error
	Similar(ctx context.Context, name string, date *time.Time) ([]domain.Event, error)
}
