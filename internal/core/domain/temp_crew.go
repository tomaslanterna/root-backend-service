package domain

import (
	"errors"
	"time"
)

// Crews temporales: grupos atados a un evento para ir juntos. Viven en la tabla `squads` con type='temporary'.
const (
	CrewTypePermanent = "permanent"
	CrewTypeTemporary = "temporary"

	TempCrewMinCapacity = 2
	TempCrewMaxCapacity = 10
	TempCrewNameMax     = 40
	TempCrewPlanMax     = 280
	TempCrewMessageMax  = 280

	JoinRequestPending   = "pending"
	JoinRequestAccepted  = "accepted"
	JoinRequestRejected  = "rejected"
	JoinRequestCancelled = "cancelled"
)

// Errores de negocio. El handler los traduce a HTTP; el repositorio y el servicio solo los devuelven.
var (
	ErrInvalidID          = errors.New("invalid id")
	ErrTempCrewNotFound   = errors.New("temporary crew not found")
	ErrEventNotFound      = errors.New("event not found")
	ErrEventEnded         = errors.New("event already ended")
	ErrAlreadyInEventCrew = errors.New("already in a temporary crew for this event")
	ErrAlreadyMember      = errors.New("already a member of this crew")
	ErrNotAdmitting       = errors.New("crew is not admitting members")
	ErrCrewFull           = errors.New("crew is full")
	ErrNotTempCrewHost    = errors.New("only the host can do this")
	ErrHostCannotLeave    = errors.New("the host cannot leave the crew")
	ErrRequestNotFound    = errors.New("join request not found")
	ErrRequestNotPending  = errors.New("join request is not pending")
	ErrTooManyRequests    = errors.New("too many join requests")
	ErrTempCrewManaged    = errors.New("temporary crews only admit members through join requests")
)

// ValidationError es un dato inválido del cliente (400).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type TempCrewPerson struct {
	UserID    string  `json:"userId"`
	Name      string  `json:"name"`
	Username  string  `json:"username"`
	AvatarURL *string `json:"avatarUrl"`
}

type TempCrewEventRef struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Date               time.Time `json:"date"`
	Location           string    `json:"location"`
	CinematicBannerURL *string   `json:"cinematicBannerUrl"`
	// Slug para el link al evento (/events/<slug>).
	Slug string `json:"slug,omitempty"`
}

// TempCrewSummary es la forma común de los listados. IsAdmitting es el valor efectivo:
// is_admitting AND integrantes < capacidad AND el evento no terminó.
type TempCrewSummary struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	Plan                 string           `json:"plan"`
	Capacity             int              `json:"capacity"`
	MemberCount          int              `json:"memberCount"`
	IsAdmitting          bool             `json:"isAdmitting"`
	Host                 TempCrewPerson   `json:"host"`
	MembersPreview       []TempCrewPerson `json:"membersPreview"`
	Event                TempCrewEventRef `json:"event"`
	ViewerRole           *string          `json:"viewerRole"`
	ViewerRequestStatus  *string          `json:"viewerRequestStatus"`
	PendingRequestsCount *int             `json:"pendingRequestsCount,omitempty"`
	// EventEnded: el evento terminó. La crew sigue en la app, pero ya no admite a nadie.
	EventEnded bool `json:"eventEnded"`
}

type TempCrewListMeta struct {
	Total        int     `json:"total"`
	Limit        int     `json:"limit"`
	Offset       int     `json:"offset"`
	HasMore      bool    `json:"hasMore"`
	ViewerCrewID *string `json:"viewerCrewId"`
}

type TempCrewListPage struct {
	Data []TempCrewSummary `json:"data"`
	Meta TempCrewListMeta  `json:"meta"`
}

// TempCrewsEventSummary es el resumen que trae GET /v1/events/{id} bajo `tempCrews`: igual a la primera página de
// GET /v1/events/{id}/temp-crews (total que admite, crew de quien consulta y las primeras crews).
type TempCrewsEventSummary struct {
	Total        int               `json:"total"`
	ViewerCrewID *string           `json:"viewerCrewId"`
	Preview      []TempCrewSummary `json:"preview"`
}

// TempCrewDetail es lo que `GET /v1/crews/:id` agrega bajo `tempCrew` cuando la crew es temporal.
type TempCrewDetail struct {
	Event                TempCrewEventRef `json:"event"`
	Plan                 string           `json:"plan"`
	Capacity             int              `json:"capacity"`
	IsAdmitting          bool             `json:"isAdmitting"`
	HostID               string           `json:"hostId"`
	ViewerRole           *string          `json:"viewerRole"`
	ViewerRequestStatus  *string          `json:"viewerRequestStatus"`
	PendingRequestsCount *int             `json:"pendingRequestsCount,omitempty"`
	EventEnded           bool             `json:"eventEnded"`
}

type JoinRequestUser struct {
	TempCrewPerson
	IsKycVerified bool `json:"isKycVerified"`
}

type TempCrewJoinRequest struct {
	ID                 string          `json:"id"`
	CrewID             string          `json:"crewId"`
	Status             string          `json:"status"`
	Message            *string         `json:"message"`
	CreatedAt          time.Time       `json:"createdAt"`
	User               JoinRequestUser `json:"user"`
	IsGoingToEvent     bool            `json:"isGoingToEvent"`
	MutualFriendsCount int             `json:"mutualFriendsCount"`
}

type CreateTempCrewInput struct {
	Name     string
	Capacity int
	Plan     string
}
