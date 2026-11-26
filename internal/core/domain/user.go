package domain

import (
	"errors"
	"time"
)

// ErrUserNotFound: no existe un usuario con ese username o ID.
var ErrUserNotFound = errors.New("user not found")

// ErrPrivateProfile: el perfil es privado y quien lo mira no lo sigue.
var ErrPrivateProfile = errors.New("private profile")

// ErrFollowRequestNotFound: no hay una solicitud pendiente de esa persona.
var ErrFollowRequestNotFound = errors.New("follow request not found")

// Estados del botón Seguir.
const (
	FollowStatusNone      = "none"
	FollowStatusRequested = "requested"
	FollowStatusFollowing = "following"
)

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	PasswordHash  string    `json:"-"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Role          string    `json:"role"`
	AvatarURL     *string   `json:"avatarUrl"`
	Dob           *string   `json:"dob"`
	DocumentID    *string   `json:"documentId"`
	Followers     []string  `json:"followers"`
	Following     []string  `json:"following"`
	IsKycVerified bool      `json:"isKycVerified"`
	Country       *string   `json:"country"`
	// IsPrivate: solo quienes lo siguen ven sus posts, insignias, eventos y listas; seguir requiere aceptar.
	IsPrivate     bool      `json:"isPrivate"`
	// TermsAcceptedAt: cuándo aceptó los Términos y la Política de privacidad (nil si todavía no).
	TermsAcceptedAt *time.Time `json:"termsAcceptedAt"`
	TermsVersion    *string    `json:"-"`
	// DeletedAt / SuspendedAt: la cuenta fue eliminada por su dueño o suspendida por moderación; no puede entrar.
	DeletedAt   *time.Time `json:"-"`
	SuspendedAt *time.Time `json:"-"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Unavailable indica si la cuenta fue eliminada o suspendida.
func (u *User) Unavailable() bool {
	return u != nil && (u.DeletedAt != nil || u.SuspendedAt != nil)
}

// FollowListUser es una fila de la lista de seguidores o seguidos de un perfil.
// IsFollowing indica si quien mira la lista ya sigue a esa persona (false sin sesión).
type FollowListUser struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Username      string  `json:"username"`
	AvatarURL     *string `json:"avatarUrl"`
	IsKycVerified bool    `json:"isKycVerified"`
	IsFollowing   bool    `json:"isFollowing"`
}
