package domain

import "errors"

// RoleRRPP es el valor de users.role (comparado sin distinguir mayúsculas) de quien puede ser RRPP oficial.
const RoleRRPP = "RRPP"

// EventRRPPMaxPosition es el máximo que admite la columna event_rrpps.position (SMALLINT).
const EventRRPPMaxPosition = 32767

var (
	ErrNotRRPP             = errors.New("user does not have the RRPP role")
	ErrInvalidRRPPPosition = errors.New("invalid RRPP position")
)

// EventRRPP es un RRPP oficial de un evento, tal como lo devuelve GET /v1/events/:id en `rrpps`.
// Solo lleva datos públicos del perfil: nunca email, teléfono ni datos de KYC.
type EventRRPP struct {
	UserID    string  `json:"userId"`
	Name      string  `json:"name"`
	Username  string  `json:"username"`
	AvatarURL *string `json:"avatarUrl"`
	Position  int     `json:"position"`
}
