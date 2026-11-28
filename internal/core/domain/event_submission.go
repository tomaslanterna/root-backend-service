package domain

import (
	"errors"
	"strings"
	"time"
)

// "Mandanos tu evento": un usuario verificado manda un evento y un ADMIN lo publica o lo rechaza.

const (
	SubmissionPending   = "pending"
	SubmissionPublished = "published"
	SubmissionRejected  = "rejected"

	SubmitterOrganizer = "organizer"
	SubmitterRRPP      = "rrpp"
	SubmitterFan       = "fan"

	SubmissionNameMax    = 120
	SubmissionVenueMax   = 120
	SubmissionAddressMax = 200
	SubmissionLineupMax  = 300
	SubmissionContactMax = 120
	SubmissionReasonMax  = 280
	SubmissionGenresMax  = 5
	// SubmissionsMaxPending: envíos pendientes por usuario.
	SubmissionsMaxPending = 5
	// SubmissionMaxLead: hasta cuánto adelante puede estar el evento.
	SubmissionMaxLead = 365 * 24 * time.Hour
	// SimilarEventsMax: candidatos de "¿Es este?".
	SimilarEventsMax = 3
)

var (
	ErrTooManyPendingSubmissions = errors.New("too many pending event submissions")
	ErrSubmissionNotFound        = errors.New("event submission not found")
	ErrSubmissionNotPending      = errors.New("event submission is not pending")
)

// EventSubmission es la forma pública del contrato (el dueño la ve en /users/me/event-submissions).
type EventSubmission struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	StartsAt     time.Time `json:"startsAt"`
	VenueName    string    `json:"venueName"`
	VenueAddress string    `json:"venueAddress"`
	// FlyerURL sale del repositorio como clave S3; el handler la firma.
	FlyerURL      *string   `json:"flyerUrl"`
	TicketURL     *string   `json:"ticketUrl"`
	Lineup        *string   `json:"lineup"`
	Genres        []string  `json:"genres"`
	SubmitterRole string    `json:"submitterRole"`
	Status        string    `json:"status"`
	RejectReason  *string   `json:"rejectReason"`
	EventID       *string   `json:"eventId"`
	CreatedAt     time.Time `json:"createdAt"`
}

// AdminEventSubmission agrega lo que necesita quien revisa: el contacto y quién lo mandó.
type AdminEventSubmission struct {
	EventSubmission
	Contact   string      `json:"contact"`
	Submitter AfterPerson `json:"submitter"`
}

type EventSubmissionInput struct {
	Name            string
	StartsAt        time.Time
	VenueName       string
	VenueAddress    string
	FlyerKey        string
	TicketURL       *string
	Lineup          *string
	Genres          []string
	SubmitterRole   string
	Contact         string
	RightsConfirmed bool
}

// NormalizeSubmission recorta y valida el envío.
func NormalizeSubmission(in EventSubmissionInput, userID string, now time.Time) (EventSubmissionInput, error) {
	out := in
	out.Name = strings.TrimSpace(in.Name)
	out.VenueName = strings.TrimSpace(in.VenueName)
	out.VenueAddress = strings.TrimSpace(in.VenueAddress)
	out.FlyerKey = strings.TrimSpace(in.FlyerKey)
	out.Contact = strings.TrimSpace(in.Contact)
	out.SubmitterRole = strings.TrimSpace(strings.ToLower(in.SubmitterRole))
	out.TicketURL = trimOptional(in.TicketURL)
	out.Lineup = trimOptional(in.Lineup)
	out.StartsAt = in.StartsAt.UTC()
	switch {
	case out.Name == "" || runeLen(out.Name) > SubmissionNameMax:
		return out, invalid("name must have between 1 and 120 characters")
	case out.VenueName == "" || runeLen(out.VenueName) > SubmissionVenueMax:
		return out, invalid("venueName must have between 1 and 120 characters")
	case out.VenueAddress == "" || runeLen(out.VenueAddress) > SubmissionAddressMax:
		return out, invalid("venueAddress must have between 1 and 200 characters")
	case out.Contact == "" || runeLen(out.Contact) > SubmissionContactMax:
		return out, invalid("contact must have between 1 and 120 characters")
	case !out.RightsConfirmed:
		return out, invalid("rightsConfirmed must be true")
	case out.StartsAt.IsZero() || out.StartsAt.Before(now.Add(-time.Hour)) || out.StartsAt.After(now.Add(SubmissionMaxLead)):
		return out, invalid("startsAt must be in the future (at most one year ahead)")
	case !ValidUploadKey(out.FlyerKey, "event-submissions", userID):
		return out, invalid("flyerKey must be an image uploaded with POST /v1/event-submissions/flyer")
	}
	switch out.SubmitterRole {
	case SubmitterOrganizer, SubmitterRRPP, SubmitterFan:
	default:
		return out, invalid("submitterRole must be organizer, rrpp or fan")
	}
	if out.TicketURL != nil && !ValidHTTPSURL(*out.TicketURL, AfterTicketURLMax) {
		return out, invalid("ticketUrl must be an https URL")
	}
	if err := optionalMax(out.Lineup, SubmissionLineupMax, "lineup must have at most 300 characters"); err != nil {
		return out, err
	}
	genres, err := NormalizeGenres(in.Genres, SubmissionGenresMax)
	if err != nil {
		return out, err
	}
	out.Genres = genres
	return out, nil
}

// NormalizeRejectReason valida el motivo del rechazo (1-280).
func NormalizeRejectReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || runeLen(reason) > SubmissionReasonMax {
		return "", invalid("reason must have between 1 and 280 characters")
	}
	return reason, nil
}
