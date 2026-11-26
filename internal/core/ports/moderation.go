package ports

import (
	"context"
	"time"

	"root-backend-service/internal/core/domain"
)

// BlockRepository guarda los bloqueos entre usuarios (tabla user_blocks).
type BlockRepository interface {
	// Block crea el bloqueo y, en la misma transacción, deja de seguirse en ambos sentidos y borra las solicitudes.
	// Es idempotente.
	Block(ctx context.Context, blockerID, blockedID string) error
	// Unblock quita el bloqueo; false si no existía.
	Unblock(ctx context.Context, blockerID, blockedID string) (bool, error)
	// ListBlocked devuelve a quiénes bloqueó blockerID, del más reciente al más viejo; nunca nil.
	ListBlocked(ctx context.Context, blockerID string) ([]domain.BlockedUser, error)
	// BlockState dice si viewerID bloqueó a otherID y si otherID bloqueó a viewerID.
	BlockState(ctx context.Context, viewerID, otherID string) (blockedByViewer, blockedViewer bool, err error)
}

// BlockService resuelve usernames y valida antes de delegar en BlockRepository.
type BlockService interface {
	Block(ctx context.Context, blockerID, targetUsername string) error
	Unblock(ctx context.Context, blockerID, targetUsername string) error
	List(ctx context.Context, blockerID string) ([]domain.BlockedUser, error)
}

// ReportRepository guarda las denuncias generales (tabla content_reports) y las acciones de moderación.
type ReportRepository interface {
	// CreateReport guarda la denuncia con una copia del contenido. created es false si esa persona ya había
	// denunciado ese contenido (devuelve el id existente). domain.ErrReportTargetNotFound si no existe o no lo puede ver.
	CreateReport(ctx context.Context, reporterID string, input domain.ReportInput) (id string, created bool, err error)
	ListReports(ctx context.Context, status string, limit int) ([]domain.ContentReport, error)
	GetReport(ctx context.Context, id string) (*domain.ContentReport, error)
	// ResolveReport marca la denuncia como actioned o dismissed.
	ResolveReport(ctx context.Context, id, status string) error
	// RemoveReportedContent oculta el post o reemplaza el texto del comentario o mensaje denunciado.
	RemoveReportedContent(ctx context.Context, report domain.ContentReport) error
	// SuspendUser impide que la cuenta vuelva a entrar e invalida sus sesiones.
	SuspendUser(ctx context.Context, userID string) error
	// PendingNotifications devuelve denuncias todavía no avisadas al soporte.
	PendingNotifications(ctx context.Context, limit int) ([]domain.ContentReport, error)
	MarkNotified(ctx context.Context, id string) error
}

// ReportService valida y crea denuncias, y avisa al soporte.
type ReportService interface {
	Create(ctx context.Context, reporterID string, input domain.ReportInput) (id string, created bool, err error)
}

// ReportNotifier avisa al equipo de soporte que llegó una denuncia (por ejemplo, por email).
type ReportNotifier interface {
	SendReportNotification(ctx context.Context, report domain.ContentReport) error
}

// AccountRepository da de baja cuentas y purga sus datos cuando vence la retención.
type AccountRepository interface {
	// DeleteAccount guarda la copia privada, anonimiza al usuario, invalida sus sesiones y oculta su contenido,
	// todo en una transacción. domain.ErrUserNotFound si no existe o ya estaba eliminado.
	DeleteAccount(ctx context.Context, userID string, retention time.Duration) error
	// DuePurges devuelve cuentas eliminadas cuya retención venció y todavía no se purgaron.
	DuePurges(ctx context.Context, limit int) ([]domain.AccountPurge, error)
	// CompletePurge borra los posts, la verificación de identidad y la copia privada de la cuenta.
	CompletePurge(ctx context.Context, userID string) error
}

// AccountService borra la cuenta de quien está logueado.
type AccountService interface {
	DeleteAccount(ctx context.Context, userID string) error
}

// ObjectDeleter borra archivos del almacenamiento (S3).
type ObjectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}
