package domain

import (
	"errors"
	"time"
)

// CurrentTermsVersion identifica la versión de Términos y Política de privacidad que se acepta al registrarse.
const CurrentTermsVersion = "2026-11"

// AccountDeletionRetention es el tiempo que se guarda la copia privada de una cuenta eliminada antes de purgarla.
const AccountDeletionRetention = 30 * 24 * time.Hour

var (
	// ErrBlocked: entre las dos personas hay un bloqueo (en cualquier sentido).
	ErrBlocked = errors.New("blocked")
	// ErrCannotBlockSelf: alguien intentó bloquearse a sí mismo.
	ErrCannotBlockSelf = errors.New("cannot block yourself")
	// ErrAccountUnavailable: la cuenta fue eliminada o suspendida y no puede iniciar sesión.
	ErrAccountUnavailable = errors.New("account unavailable")
	// ErrTermsNotAccepted: el registro llegó sin aceptar los Términos y la Política de privacidad.
	ErrTermsNotAccepted = errors.New("terms not accepted")
	// ErrInvalidReport: la denuncia tiene un tipo, motivo o id inválido.
	ErrInvalidReport = errors.New("invalid report")
	// ErrReportTargetNotFound: el contenido denunciado no existe o quien denuncia no puede verlo.
	ErrReportTargetNotFound = errors.New("report target not found")
	// ErrReportNotFound: no existe la denuncia pedida (comando de moderación).
	ErrReportNotFound = errors.New("report not found")
)

// ReportTargetType es el tipo de contenido denunciado.
type ReportTargetType string

const (
	ReportTargetPost    ReportTargetType = "post"
	ReportTargetComment ReportTargetType = "comment"
	ReportTargetMessage ReportTargetType = "message"
	ReportTargetUser    ReportTargetType = "user"
)

// Valid indica si el tipo es uno de los admitidos.
func (t ReportTargetType) Valid() bool {
	switch t {
	case ReportTargetPost, ReportTargetComment, ReportTargetMessage, ReportTargetUser:
		return true
	}
	return false
}

// Motivos de denuncia: los mismos que las denuncias de comunidades.
const (
	ReportReasonSpam  = "spam"
	ReportReasonAbuse = "abuse"
	ReportReasonOther = "other"
)

// Estados de una denuncia.
const (
	ReportStatusPending   = "pending"
	ReportStatusActioned  = "actioned"
	ReportStatusDismissed = "dismissed"
)

// MaxReportDetails es el largo máximo del detalle opcional de una denuncia.
const MaxReportDetails = 1000

// ReportInput es lo que manda quien denuncia.
type ReportInput struct {
	TargetType ReportTargetType `json:"targetType"`
	TargetID   string           `json:"targetId"`
	Reason     string           `json:"reason"`
	Details    string           `json:"details"`
}

// ContentReport es una denuncia guardada. Solo la ve el equipo de moderación (email y cmd/moderation).
type ContentReport struct {
	ID               string           `json:"id"`
	ReporterID       *string          `json:"reporterId"`
	ReporterUsername string           `json:"reporterUsername"`
	TargetType       ReportTargetType `json:"targetType"`
	TargetID         string           `json:"targetId"`
	TargetUserID     *string          `json:"targetUserId"`
	TargetUsername   string           `json:"targetUsername"`
	Reason           string           `json:"reason"`
	Details          string           `json:"details"`
	ContentSnapshot  string           `json:"contentSnapshot"`
	Status           string           `json:"status"`
	CreatedAt        time.Time        `json:"createdAt"`
	ReviewedAt       *time.Time       `json:"reviewedAt"`
}

// BlockedUser es una fila de "Usuarios bloqueados".
type BlockedUser struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	AvatarURL *string   `json:"avatarUrl"`
	BlockedAt time.Time `json:"blockedAt"`
}

// DeletedAccountSnapshot es la copia privada de una cuenta eliminada, que se guarda hasta la purga.
type DeletedAccountSnapshot struct {
	Email      string  `json:"email"`
	Name       string  `json:"name"`
	Username   string  `json:"username"`
	Dob        *string `json:"dob,omitempty"`
	DocumentID *string `json:"documentId,omitempty"`
	Country    *string `json:"country,omitempty"`
	AvatarURL  *string `json:"avatarUrl,omitempty"`
	// ObjectKeys son las claves de S3 (avatar, posts y verificación de identidad) que se borran en la purga.
	ObjectKeys []string `json:"objectKeys"`
}

// AccountPurge es una cuenta eliminada cuyo plazo de retención venció.
type AccountPurge struct {
	UserID     string
	ObjectKeys []string
}
