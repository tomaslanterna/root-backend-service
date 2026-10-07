package domain

import (
	"errors"
	"time"
)

var (
	ErrCommunityNotFound       = errors.New("community not found")
	ErrCommunityForbidden      = errors.New("community publishing forbidden")
	ErrCommunityInvalid        = errors.New("invalid community request")
	ErrCommunityTargetNotFound = errors.New("community content not found")
)

type Community struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Slug          string            `json:"slug"`
	Category      string            `json:"category"`
	Zone          string            `json:"zone"`
	PROwnerID     *string           `json:"prOwnerId,omitempty"`
	CountryID     string            `json:"countryId"`
	CoverImageURL string            `json:"coverImageUrl"`
	Description   string            `json:"description"`
	CreatedAt     time.Time         `json:"createdAt"`
	MembersCount  int               `json:"membersCount"`
	IsMember      bool              `json:"isMember"`
	CanPublish    bool              `json:"canPublish"`
	IsActive      bool              `json:"isActive"`
	UnreadCount   int               `json:"unreadCount"`
	Muted         bool              `json:"muted"`
	Contact       *CommunityContact `json:"contact,omitempty"`
}

type CommunityContact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl"`
}

type CommunityReportInput struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Reason     string `json:"reason"`
	Details    string `json:"details"`
}

type CommunityReport struct {
	ID string `json:"id"`
	CommunityReportInput
	Content      string    `json:"content"`
	ReporterName string    `json:"reporterName"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

type CommunityFilter struct {
	Country    string
	Category   string
	Department string
	Query      string
	Limit      int
	Offset     int
	Scope      string
	ViewerID   string
}
