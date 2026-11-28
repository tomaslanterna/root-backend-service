package domain

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Afters: fiestas posteriores que suben usuarios con la identidad verificada. Contrato y reglas en AFTERS.md.

const (
	AfterAccessFree     = "free"
	AfterAccessApproval = "approval"
	AfterAccessTicket   = "ticket"

	AfterStatusActive  = "active"
	AfterStatusHidden  = "hidden"
	AfterStatusRemoved = "removed"
	// AfterStatusArchived no se guarda: es el estado que ve el host cuando pasaron AfterArchiveAfter desde el inicio.
	AfterStatusArchived = "archived"

	AttendeeRequested = "requested"
	AttendeeAccepted  = "accepted"
	AttendeeRejected  = "rejected"
	AttendeeGoing     = "going"
	AttendeeCancelled = "cancelled"

	// ViewerStatusHost es el viewerStatus de quien subió el after.
	ViewerStatusHost = "host"
)

// Límites de los campos (caracteres, no bytes).
const (
	AfterNameMax      = 60
	AfterZoneMax      = 60
	AfterAddressMax   = 200
	AfterLineupMax    = 120
	AfterRulesMax     = 500
	AfterNoticeMax    = 280
	AfterMessageMax   = 280
	AfterReasonMax    = 280
	AfterPriceMax     = 40
	AfterTicketURLMax = 500
	AfterGenresMax    = 5
	AfterGenreMax     = 30
	AfterCapacityMin  = 2
	AfterCapacityMax  = 500
)

// Reglas de negocio. Cada decisión por defecto de la spec es una constante para poder cambiarla en un solo lugar.
const (
	// AfterArchiveAfter: un after se archiva solo a las 24 h de empezar (deja de verse, salvo para el host).
	AfterArchiveAfter = 24 * time.Hour
	// AfterDefaultDuration: sin endsAt, un after se considera en curso hasta 12 h después de empezar.
	AfterDefaultDuration = 12 * time.Hour
	// AfterMaxLead: startsAt puede estar como mucho 7 días adelante.
	AfterMaxLead = 7 * 24 * time.Hour
	// AfterStartGrace tolera relojes un poco atrasados y el atajo "ya empezó" del formulario.
	AfterStartGrace = 15 * time.Minute
	// AfterEventGrace: se puede atar un after a un evento hasta 12 h después de que terminó.
	AfterEventGrace = 12 * time.Hour
	// EventDefaultDuration: un evento sin endDate termina 12 h después de empezar (igual que el resto del backend).
	EventDefaultDuration = 12 * time.Hour
	// AftersMaxActivePerHost: afters activos (status active y todavía no terminados) por host.
	AftersMaxActivePerHost = 3
	// AfterReportsToHide: con esta cantidad de denuncias de usuarios distintos el after pasa a 'hidden'.
	AfterReportsToHide = 3
	// AfterEventPreview: afters que trae el detalle del evento en `afters.preview`.
	AfterEventPreview = 3
	// AfterFollowedPreview: personas que seguís y van, en `followedGoing.preview`.
	AfterFollowedPreview = 4
	// AfterMinAge: edad mínima (según users.dob) para ver un after, pedir entrar o marcar "Voy".
	AfterMinAge = 18
)

var (
	ErrAfterNotFound        = errors.New("after not found")
	ErrNotAfterHost         = errors.New("only the host can do this")
	ErrAfterFull            = errors.New("after is full")
	ErrAlreadyJoined        = errors.New("already joined this after")
	ErrAfterEnded           = errors.New("after already ended")
	ErrUnderage             = errors.New("viewer is under the minimum age")
	ErrKycRequired          = errors.New("identity verification required")
	ErrAdminRequired        = errors.New("admin role required")
	ErrTooManyActiveAfters  = errors.New("too many active afters")
	ErrInvalidEvent         = errors.New("event does not exist or ended too long ago")
	ErrAfterRequestNotFound = errors.New("after join request not found")
	ErrAfterRequestDecided  = errors.New("after join request already decided")
	ErrHostCannotLeaveAfter = errors.New("the host cannot leave the after")
	ErrCannotReportOwnAfter = errors.New("the host cannot report their own after")
)

// ViewerAccess es lo mínimo que hace falta para autorizar: verificación de identidad, edad, rol y estado de la cuenta.
// Nunca se loguea (dob es un dato personal).
type ViewerAccess struct {
	UserID        string
	IsKycVerified bool
	Dob           string
	Role          string
	Unavailable   bool
}

// IsAdmin: users.role = 'ADMIN'.
func (v ViewerAccess) IsAdmin() bool { return strings.EqualFold(strings.TrimSpace(v.Role), "ADMIN") }

var montevideo = func() *time.Location {
	if loc, err := time.LoadLocation("America/Montevideo"); err == nil {
		return loc
	}
	return time.FixedZone("UYT", -3*60*60)
}()

// AgeOn calcula la edad según users.dob ("YYYY-MM-DD"; se acepta un sufijo de hora como "YYYY-MM-DDT..."), en el
// calendario de Montevideo. ok es false si la fecha falta o no se puede leer: quien llama la trata como no
// verificable (decisión: se responde 403 "underage", fail-closed).
func AgeOn(dob string, now time.Time) (age int, ok bool) {
	dob = strings.TrimSpace(dob)
	if len(dob) < 10 {
		return 0, false
	}
	born, err := time.Parse("2006-01-02", dob[:10])
	if err != nil || (len(dob) > 10 && dob[10] != 'T' && dob[10] != ' ') {
		return 0, false
	}
	today := now.In(montevideo)
	if born.Year() < 1900 || born.After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)) {
		return 0, false
	}
	age = today.Year() - born.Year()
	if today.Month() < born.Month() || (today.Month() == born.Month() && today.Day() < born.Day()) {
		age--
	}
	return age, true
}

// IsAdult: la edad se puede verificar y es al menos AfterMinAge.
func IsAdult(dob string, now time.Time) bool {
	age, ok := AgeOn(dob, now)
	return ok && age >= AfterMinAge
}

type AfterPerson struct {
	UserID    string  `json:"userId"`
	Name      string  `json:"name"`
	Username  string  `json:"username"`
	AvatarURL *string `json:"avatarUrl"`
}

type AfterHost struct {
	AfterPerson
	IsKycVerified bool `json:"isKycVerified"`
}

type AfterEventRef struct {
	ID       string    `json:"id"`
	Slug     string    `json:"slug,omitempty"`
	Title    string    `json:"title"`
	Date     time.Time `json:"date"`
	Location string    `json:"location"`
	// Image es la clave S3 o URL del flyer del evento; el handler la firma.
	Image *string `json:"image"`
}

// AfterSummary es la forma de los listados y del embed del evento. Nunca lleva la dirección.
type AfterSummary struct {
	ID string `json:"id"`
	// CoverURL sale del repositorio como clave S3 y el handler la convierte en URL firmada.
	CoverURL      *string        `json:"coverUrl"`
	Name          string         `json:"name"`
	StartsAt      time.Time      `json:"startsAt"`
	EndsAt        *time.Time     `json:"endsAt"`
	ArchivesAt    time.Time      `json:"archivesAt"`
	Zone          string         `json:"zone"`
	Access        string         `json:"access"`
	Price         *string        `json:"price"`
	TicketURL     *string        `json:"ticketUrl"`
	Capacity      *int           `json:"capacity"`
	AcceptedCount int            `json:"acceptedCount"`
	GoingCount    int            `json:"goingCount"`
	IsOfficial    bool           `json:"isOfficial"`
	Host          AfterHost      `json:"host"`
	Event         *AfterEventRef `json:"event"`
	Genres        []string       `json:"genres"`
	ViewerStatus  *string        `json:"viewerStatus"`
}

type AfterFollowedGoing struct {
	Count   int           `json:"count"`
	Preview []AfterPerson `json:"preview"`
}

// AfterDetail es GET /v1/afters/:id. Address y Notice solo se completan con ForViewer.
type AfterDetail struct {
	AfterSummary
	Rules           *string            `json:"rules"`
	Lineup          *string            `json:"lineup"`
	AddressVisible  bool               `json:"addressVisible"`
	Address         *string            `json:"address"`
	Notice          *string            `json:"notice"`
	NoticeUpdatedAt *time.Time         `json:"noticeUpdatedAt"`
	FollowedGoing   AfterFollowedGoing `json:"followedGoing"`
	CanEdit         bool               `json:"canEdit"`
	// Status (agregado al contrato): active | hidden | removed | archived. Le sirve al host, que es el único que ve
	// su after oculto o archivado.
	Status string `json:"status"`
}

// AfterDetailRow es lo que lee el repositorio: el detalle sin los datos privados aplicados todavía.
type AfterDetailRow struct {
	Detail          AfterDetail
	Address         string
	Notice          *string
	NoticeUpdatedAt *time.Time
	// StoredStatus es afters.status (active/hidden/removed); Archived se calcula con la fecha.
	StoredStatus string
	Archived     bool
	Ended        bool
}

// CanSeeAddress: el host, cualquiera si el acceso es free o ticket, o quien fue aceptado.
func CanSeeAddress(access string, viewerStatus *string) bool {
	if viewerStatus != nil && (*viewerStatus == ViewerStatusHost || *viewerStatus == AttendeeAccepted) {
		return true
	}
	return access == AfterAccessFree || access == AfterAccessTicket
}

// CanSeeNotice: el host y quienes fueron aceptados o van.
func CanSeeNotice(viewerStatus *string) bool {
	if viewerStatus == nil {
		return false
	}
	switch *viewerStatus {
	case ViewerStatusHost, AttendeeAccepted, AttendeeGoing:
		return true
	}
	return false
}

// ForViewer aplica la privacidad: la dirección y el aviso solo salen a quien corresponde. Nunca se manda un texto
// del tipo "oculta": si no la puede ver, address es null.
func (r AfterDetailRow) ForViewer() AfterDetail {
	d := r.Detail
	d.Address, d.Notice, d.NoticeUpdatedAt = nil, nil, nil
	d.AddressVisible = CanSeeAddress(d.Access, d.ViewerStatus)
	if d.AddressVisible && r.Address != "" {
		address := r.Address
		d.Address = &address
	}
	if CanSeeNotice(d.ViewerStatus) && r.Notice != nil {
		notice := *r.Notice
		d.Notice = &notice
		d.NoticeUpdatedAt = r.NoticeUpdatedAt
	}
	isHost := d.ViewerStatus != nil && *d.ViewerStatus == ViewerStatusHost
	d.CanEdit = isHost && r.StoredStatus != AfterStatusRemoved && !r.Archived
	d.Status = r.StoredStatus
	if r.Archived && r.StoredStatus == AfterStatusActive {
		d.Status = AfterStatusArchived
	}
	if d.Genres == nil {
		d.Genres = []string{}
	}
	if d.FollowedGoing.Preview == nil {
		d.FollowedGoing.Preview = []AfterPerson{}
	}
	return d
}

// AftersEventSummary es el embed `afters` de GET /v1/events/:id.
type AftersEventSummary struct {
	Total          int            `json:"total"`
	Preview        []AfterSummary `json:"preview"`
	ViewerAfterIDs []string       `json:"viewerAfterIds"`
}

type AfterPageMeta struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

type AfterPage struct {
	Data []AfterSummary `json:"data"`
	Meta AfterPageMeta  `json:"meta"`
}

// AfterFilter es GET /v1/afters: afters próximos o en curso que se solapan con [From, Until].
type AfterFilter struct {
	From   time.Time
	Until  *time.Time
	Zone   string
	Limit  int
	Offset int
}

type AfterRequest struct {
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	AvatarURL *string   `json:"avatarUrl"`
	Message   *string   `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
	Status    string    `json:"status"`
}

// AdminAfter es una fila de GET /v1/admin/afters: el resumen más el estado y las denuncias (sin la dirección).
type AdminAfter struct {
	AfterSummary
	Status       string   `json:"status"`
	ReportsCount int      `json:"reportsCount"`
	Reasons      []string `json:"reasons"`
}

// AfterEventWindow es el evento al que se ata un after.
type AfterEventWindow struct {
	ID       string
	StartsAt time.Time
	EndsAt   time.Time
}

// AfterFields son los datos editables de un after, ya normalizados.
type AfterFields struct {
	Name      string
	EventID   *string
	StartsAt  time.Time
	EndsAt    *time.Time
	Zone      string
	Address   string
	Access    string
	Price     *string
	TicketURL *string
	Capacity  *int
	Genres    []string
	Lineup    *string
	Rules     *string
	CoverKey  *string
}

// AfterCurrent es el after tal como está, para aplicar un PATCH.
type AfterCurrent struct {
	Fields       AfterFields
	HostID       string
	StoredStatus string
	Archived     bool
	Notice       *string
}

// Nullable es un campo de PATCH: Set indica que vino en el body; Set con Value nil significa "borrarlo".
type Nullable[T any] struct {
	Set   bool
	Value *T
}

// AfterPatch es el body de PATCH /v1/afters/:id. Los campos obligatorios no se pueden borrar (null = no cambia).
type AfterPatch struct {
	Name      *string
	EventID   Nullable[string]
	StartsAt  *time.Time
	EndsAt    Nullable[time.Time]
	Zone      *string
	Address   *string
	Access    *string
	Price     Nullable[string]
	TicketURL Nullable[string]
	Capacity  Nullable[int]
	Genres    *[]string
	Lineup    Nullable[string]
	Rules     Nullable[string]
	CoverKey  Nullable[string]
	Notice    Nullable[string]
}

// Apply devuelve los campos con el PATCH aplicado (sin validar).
func (p AfterPatch) Apply(current AfterFields) AfterFields {
	next := current
	if p.Name != nil {
		next.Name = *p.Name
	}
	if p.EventID.Set {
		next.EventID = p.EventID.Value
	}
	if p.StartsAt != nil {
		next.StartsAt = *p.StartsAt
	}
	if p.EndsAt.Set {
		next.EndsAt = p.EndsAt.Value
	}
	if p.Zone != nil {
		next.Zone = *p.Zone
	}
	if p.Address != nil {
		next.Address = *p.Address
	}
	if p.Access != nil {
		next.Access = *p.Access
	}
	if p.Price.Set {
		next.Price = p.Price.Value
	}
	if p.TicketURL.Set {
		next.TicketURL = p.TicketURL.Value
	}
	if p.Capacity.Set {
		next.Capacity = p.Capacity.Value
	}
	if p.Genres != nil {
		next.Genres = *p.Genres
	}
	if p.Lineup.Set {
		next.Lineup = p.Lineup.Value
	}
	if p.Rules.Set {
		next.Rules = p.Rules.Value
	}
	if p.CoverKey.Set {
		next.CoverKey = p.CoverKey.Value
	}
	return next
}

// NoticeChange describe el cambio del aviso en un PATCH. Changed con Value nil borra el aviso.
type NoticeChange struct {
	Changed bool
	Value   *string
}

func invalid(message string) error { return &ValidationError{Message: message} }

// trimOptional deja nil los textos vacíos.
func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func runeLen(value string) int { return utf8.RuneCountInString(value) }

func optionalMax(value *string, max int, message string) error {
	if value != nil && runeLen(*value) > max {
		return invalid(message)
	}
	return nil
}

// ValidHTTPSURL acepta solo URLs https absolutas con host.
func ValidHTTPSURL(raw string, max int) bool {
	if runeLen(raw) > max || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

// ValidUploadKey exige que la clave S3 esté bajo el prefijo del usuario (nadie puede usar archivos ajenos).
func ValidUploadKey(key, prefix, userID string) bool {
	expected := prefix + "/" + userID + "/"
	return strings.HasPrefix(key, expected) && len(key) > len(expected) && len(key) <= 300 &&
		!strings.Contains(key, "..") && !strings.ContainsAny(key, " \\\t\r\n?#")
}

// NormalizeGenres recorta, descarta vacíos y repetidos (sin distinguir mayúsculas) y valida la cantidad.
func NormalizeGenres(genres []string, max int) ([]string, error) {
	out := make([]string, 0, len(genres))
	seen := map[string]bool{}
	for _, genre := range genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		if runeLen(genre) > AfterGenreMax {
			return nil, invalid("each genre must have at most 30 characters")
		}
		key := strings.ToLower(genre)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, genre)
	}
	if len(out) > max {
		return nil, invalid("genres must have at most 5 items")
	}
	return out, nil
}

// AfterValidation dice qué reglas que dependen del momento hay que revisar (en un PATCH solo si el dato cambió).
type AfterValidation struct {
	Now          time.Time
	HostID       string
	CheckStart   bool
	Event        *AfterEventWindow
	CheckEventTo bool
}

// NormalizeAfter recorta y valida los campos. Devuelve una copia normalizada.
func NormalizeAfter(in AfterFields, v AfterValidation) (AfterFields, error) {
	out := in
	out.Name = strings.TrimSpace(in.Name)
	out.Zone = strings.TrimSpace(in.Zone)
	out.Address = strings.TrimSpace(in.Address)
	out.Access = strings.TrimSpace(strings.ToLower(in.Access))
	out.Price = trimOptional(in.Price)
	out.TicketURL = trimOptional(in.TicketURL)
	out.Lineup = trimOptional(in.Lineup)
	out.Rules = trimOptional(in.Rules)
	out.CoverKey = trimOptional(in.CoverKey)
	out.EventID = trimOptional(in.EventID)
	out.StartsAt = in.StartsAt.UTC()
	if in.EndsAt != nil {
		end := in.EndsAt.UTC()
		out.EndsAt = &end
	}

	switch {
	case out.Name == "" || runeLen(out.Name) > AfterNameMax:
		return out, invalid("name must have between 1 and 60 characters")
	case out.Zone == "" || runeLen(out.Zone) > AfterZoneMax:
		return out, invalid("zone must have between 1 and 60 characters")
	case out.Address == "" || runeLen(out.Address) > AfterAddressMax:
		return out, invalid("address must have between 1 and 200 characters")
	}
	if err := optionalMax(out.Lineup, AfterLineupMax, "lineup must have at most 120 characters"); err != nil {
		return out, err
	}
	if err := optionalMax(out.Rules, AfterRulesMax, "rules must have at most 500 characters"); err != nil {
		return out, err
	}
	if out.Capacity != nil && (*out.Capacity < AfterCapacityMin || *out.Capacity > AfterCapacityMax) {
		return out, invalid("capacity must be between 2 and 500")
	}
	genres, err := NormalizeGenres(in.Genres, AfterGenresMax)
	if err != nil {
		return out, err
	}
	out.Genres = genres

	switch out.Access {
	case AfterAccessFree, AfterAccessApproval:
		// El precio y el link de compra solo tienen sentido con entrada.
		out.Price, out.TicketURL = nil, nil
	case AfterAccessTicket:
		if out.TicketURL == nil {
			return out, invalid("ticketUrl is required when access is ticket")
		}
		if !ValidHTTPSURL(*out.TicketURL, AfterTicketURLMax) {
			return out, invalid("ticketUrl must be an https URL")
		}
		if err := optionalMax(out.Price, AfterPriceMax, "price must have at most 40 characters"); err != nil {
			return out, err
		}
	default:
		return out, invalid("access must be free, approval or ticket")
	}
	if out.CoverKey != nil && !ValidUploadKey(*out.CoverKey, "afters", v.HostID) {
		return out, invalid("coverKey must be an image uploaded with POST /v1/afters/cover")
	}

	if out.StartsAt.IsZero() {
		return out, invalid("startsAt is required")
	}
	if v.CheckStart {
		if out.StartsAt.Before(v.Now.Add(-AfterStartGrace)) || out.StartsAt.After(v.Now.Add(AfterMaxLead)) {
			return out, invalid("startsAt must be between now and 7 days ahead")
		}
	}
	if out.EndsAt != nil && (!out.EndsAt.After(out.StartsAt) || out.EndsAt.After(out.StartsAt.Add(AfterArchiveAfter))) {
		return out, invalid("endsAt must be after startsAt and at most 24 hours later")
	}
	if v.CheckEventTo && out.EventID != nil {
		if v.Event == nil || v.Now.After(v.Event.EndsAt.Add(AfterEventGrace)) {
			return out, ErrInvalidEvent
		}
		if out.StartsAt.Before(v.Event.StartsAt) {
			return out, invalid("startsAt must be at or after the start of the event")
		}
	}
	return out, nil
}

// NormalizeNotice valida el aviso del host (≤ 280). Vacío equivale a borrarlo.
func NormalizeNotice(value *string) (*string, error) {
	notice := trimOptional(value)
	if err := optionalMax(notice, AfterNoticeMax, "notice must have at most 280 characters"); err != nil {
		return nil, err
	}
	return notice, nil
}
