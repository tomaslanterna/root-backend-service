package ports

import (
	"context"

	"root-backend-service/internal/core/domain"
)

// TempCrewRepository persiste las crews temporales y sus solicitudes. Cada método que escribe es una
// única transacción; la identidad (userID/hostID) llega siempre del JWT, nunca del body.
type TempCrewRepository interface {
	InitSchema(context.Context) error
	ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.TempCrewListPage, error)
	// EventSummary: resumen para el detalle del evento (no valida que el evento exista).
	EventSummary(ctx context.Context, eventID, viewerID string, limit int) (*domain.TempCrewsEventSummary, error)
	Create(ctx context.Context, hostID, eventID string, input domain.CreateTempCrewInput) (string, error)
	Summary(ctx context.Context, crewID, viewerID string) (*domain.TempCrewSummary, error)
	// Detail devuelve (nil, nil) si la crew existe pero no es temporal.
	Detail(ctx context.Context, crewID, viewerID string) (*domain.TempCrewDetail, error)
	// RequestJoin devuelve la solicitud y si fue creada ahora (false: ya había una pendiente).
	RequestJoin(ctx context.Context, crewID, userID string, message *string) (domain.TempCrewJoinRequest, bool, error)
	CancelMyRequest(ctx context.Context, crewID, userID string) error
	ListRequests(ctx context.Context, crewID, hostID, status string, limit, offset int) ([]domain.TempCrewJoinRequest, error)
	Decide(ctx context.Context, crewID, requestID, hostID string, accept bool) (domain.TempCrewJoinRequest, error)
	SetAdmitting(ctx context.Context, crewID, hostID string, admitting bool) (bool, error)
	ListMine(ctx context.Context, userID string) ([]domain.TempCrewSummary, error)
	Discover(ctx context.Context, userID string, limit int) ([]domain.TempCrewSummary, error)
	// CloseEnded cancela las solicitudes pendientes de las crews cuyo evento terminó. Devuelve cuántas canceló.
	CloseEnded(ctx context.Context) (int64, error)
}

// TempCrewService valida la entrada y delega en el repositorio.
type TempCrewService interface {
	ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.TempCrewListPage, error)
	// EventSummary trae las primeras crews que admiten gente, para el detalle del evento.
	EventSummary(ctx context.Context, eventID, viewerID string) (*domain.TempCrewsEventSummary, error)
	Create(ctx context.Context, hostID, eventID string, input domain.CreateTempCrewInput) (*domain.TempCrewSummary, error)
	Detail(ctx context.Context, crewID, viewerID string) (*domain.TempCrewDetail, error)
	RequestJoin(ctx context.Context, crewID, userID, message string) (domain.TempCrewJoinRequest, bool, error)
	CancelMyRequest(ctx context.Context, crewID, userID string) error
	ListRequests(ctx context.Context, crewID, hostID, status string, limit, offset int) ([]domain.TempCrewJoinRequest, error)
	Decide(ctx context.Context, crewID, requestID, hostID string, accept bool) (domain.TempCrewJoinRequest, error)
	SetAdmitting(ctx context.Context, crewID, hostID string, admitting bool) (bool, error)
	ListMine(ctx context.Context, userID string) ([]domain.TempCrewSummary, error)
	Discover(ctx context.Context, userID string, limit int) ([]domain.TempCrewSummary, error)
	CloseEnded(ctx context.Context) (int64, error)
}
