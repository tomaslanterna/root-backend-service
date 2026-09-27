package domain

import (
	"time"
)

type EventSwipe struct {
	ID          string                 `json:"id"`
	UserID      string                 `json:"user_id"`
	EventID     string                 `json:"event_id"`
	Direction   string                 `json:"direction"`
	HasCrew     bool                   `json:"has_crew"`
	Preferences map[string]interface{} `json:"preferences"`
	CreatedAt   time.Time              `json:"created_at"`
}

type Squad struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Type       string    `json:"type"`        // 'event_match' or 'permanent'
	InviteCode *string   `json:"invite_code"` // For permanent crews
	ChatRoomID string    `json:"chat_room_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type SquadMember struct {
	SquadID   string    `json:"squad_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
	HasTicket bool      `json:"has_ticket"`
}

type VibePreferences struct {
	UserID          string   `json:"user_id"`
	EnergyLevel     string   `json:"energy_level"`
	AgeRange        string   `json:"age_range"`
	Budget          string   `json:"budget"`
	ArrivalTime     string   `json:"arrival_time"`
	TicketStatus    string   `json:"ticket_status"`
	Transport       string   `json:"transport"`
	SocialVibe      string   `json:"social_vibe"`
	FavoriteGenres  []string `json:"favorite_genres"`
	PartyStyle      string   `json:"party_style"`
	VerifiedKYCOnly bool     `json:"verified_kyc_only"`
	SpotifyConnected bool    `json:"spotify_connected"`
}
