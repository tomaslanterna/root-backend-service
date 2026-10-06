package domain

import (
	"errors"
	"time"
)

var (
	ErrCommunityNotFound  = errors.New("community not found")
	ErrCommunityForbidden = errors.New("community publishing forbidden")
)

type Community struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Category      string    `json:"category"`
	Zone          string    `json:"zone"`
	PROwnerID     *string   `json:"prOwnerId,omitempty"`
	CountryID     string    `json:"countryId"`
	CoverImageURL string    `json:"coverImageUrl"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"createdAt"`
	MembersCount  int       `json:"membersCount"`
	IsMember      bool      `json:"isMember"`
	CanPublish    bool      `json:"canPublish"`
	IsActive      bool      `json:"isActive"`
}

type CommunityFilter struct {
	Country    string
	Category   string
	Department string
	Query      string
	Limit      int
	Offset     int
}
