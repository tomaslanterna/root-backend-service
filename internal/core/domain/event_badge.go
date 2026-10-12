package domain

import (
	"errors"
	"time"
)

var ErrAttendanceRequired = errors.New("confirmed attendance required")
var ErrInvalidSurvey = errors.New("invalid survey")
var ErrSurveyConfirmed = errors.New("survey already confirmed")

// One confirmed attendance is one collectible. No parallel badge ledger is needed.
type BadgeHeadliner struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ImageURL string `json:"imageUrl"`
}

type EventBadge struct {
	EventID     string           `json:"eventId"`
	Title       string           `json:"title"`
	ImageURL    string           `json:"imageUrl"`
	EventDate   time.Time        `json:"eventDate"`
	Location    string           `json:"location"`
	Genre       *string          `json:"genre"`
	Headliner   *BadgeHeadliner  `json:"headliner"`
	ConfirmedAt time.Time        `json:"confirmedAt"`
	Source      string           `json:"source"`
	DepartedAt  *time.Time       `json:"departedAt,omitempty"`
	Department  *string          `json:"department,omitempty"`
	Country     *string          `json:"country,omitempty"`
	Headliners  []BadgeHeadliner `json:"headliners,omitempty"`
	Review      *EventSurvey     `json:"review"`
	Steps       *int64           `json:"steps"`
	MostDanced  bool             `json:"mostDanced"`
	LineupNames []string         `json:"lineupNames,omitempty"`
	Lineup      []EventArtist    `json:"lineup,omitempty"`
}
