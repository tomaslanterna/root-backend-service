// Package eventimport finds upcoming electronic-music events in Uruguay and
// loads them into the events table with flyer, venue, coordinates, lineup and
// ticket info. Everything an LLM returns is treated as untrusted input: dates,
// URLs, images and coordinates are verified by code before anything is stored.
package eventimport

import "time"

// Candidate is a raw event as extracted by the LLM. Nothing here is trusted.
type Candidate struct {
	Title       string   `json:"title"`
	StartsAt    string   `json:"startsAt"` // local Uruguay time, e.g. 2026-12-05T23:00
	EndsAt      string   `json:"endsAt"`
	VenueName   string   `json:"venueName"`
	Address     string   `json:"address"`
	City        string   `json:"city"`
	Genre       string   `json:"genre"`
	Lineup      []string `json:"lineup"`
	Description string   `json:"description"`
	EventURL    string   `json:"eventUrl"`
	TicketURL   string   `json:"ticketUrl"`

	// Sources are the pages the search consulted. Set by code, never by the LLM.
	Sources []string `json:"-"`
}

// Event is a validated candidate, ready to be enriched.
type Event struct {
	Title       string
	StartsAt    time.Time // wall-clock Uruguay time stored as-is (events.date has no zone)
	EndsAt      *time.Time
	VenueName   string
	Address     string
	City        string
	Genre       string
	Lineup      []string
	Description string
	EventURL    string
	TicketURL   string
}

// Place is a resolved location.
type Place struct {
	Lat, Lng   float64
	City       string
	Department string
}

// Window is the date range being searched, in Uruguay local time.
type Window struct {
	From, To time.Time
}

// NewEvent is everything the store needs to insert one event.
type NewEvent struct {
	Event
	SourceURL    string
	Place        Place
	HasPlace     bool
	BannerKey    string
	TicketSource string
	Tiers        []Tier
	TiersFetched bool
}

// Tier mirrors domain.TicketTier without importing core.
type Tier struct {
	Name         string  `json:"name"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
	SoldOut      bool    `json:"soldOut"`
	FewRemaining bool    `json:"fewRemaining"`
}

// Existing is an event already stored, used for deduplication and venue reuse.
type Existing struct {
	ID        string
	Title     string
	Location  string
	Date      time.Time
	SourceURL string
}

// KnownVenue is a venue we already have coordinates for.
type KnownVenue struct {
	Lat, Lng   float64
	City       string
	Department string
}
