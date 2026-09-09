package domain

import (
	"encoding/json"
	"time"
)

type Artist struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	ArtistType  string          `json:"artistType"` // DJ, Band, Producer, etc.
	Genres      []string        `json:"genres"`
	AvatarURL   *string         `json:"avatarUrl,omitempty"`
	SocialLinks json.RawMessage `json:"socialLinks,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type EventArtist struct {
	EventID         string     `json:"eventId"`
	ArtistID        string     `json:"artistId"`
	PerformanceTime *time.Time `json:"performanceTime,omitempty"`
	IsHeadliner     bool       `json:"isHeadliner"`
	
	// This embeds the Artist details when querying Event Lineups
	Artist *Artist `json:"artist,omitempty"`
}
