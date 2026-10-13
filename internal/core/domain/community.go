package domain

import (
	"errors"
	"time"
)

var (
	ErrCommunityNotFound          = errors.New("community not found")
	ErrCommunityForbidden         = errors.New("community publishing forbidden")
	ErrCommunityInvalid           = errors.New("invalid community request")
	ErrCommunityTargetNotFound    = errors.New("community content not found")
	ErrCommunityCannotKickOwner   = errors.New("cannot kick community owner")
	ErrCommunityReactionNotAllowed = errors.New("reactions only allowed on RRPP messages")
	ErrCommunityChannelClosed     = errors.New("channel is closed for member messages")
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

type CommunityChatSettings struct {
	CommunityID string    `json:"communityId"`
	Mode        string    `json:"mode"`
	UpdatedAt   time.Time `json:"updatedAt"`
	UpdatedBy   *string   `json:"updatedBy,omitempty"`
}

type CommunityChatReaction struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

type CommunityChatMessage struct {
	ID             string                  `json:"id"`
	CommunityID    string                  `json:"communityId"`
	SenderID       string                  `json:"senderId"`
	SenderName     string                  `json:"senderName"`
	SenderUsername string                  `json:"senderUsername,omitempty"`
	SenderAvatar   string                  `json:"senderAvatar,omitempty"`
	SenderRole     string                  `json:"senderRole"` // "RRPP" | "ADMIN" | "MEMBER"
	Content        string                  `json:"content"`
	Timestamp      time.Time               `json:"timestamp"`
	Reactions      []CommunityChatReaction `json:"reactions"`
}

type CommunityMember struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	AvatarURL     string    `json:"avatarUrl"`
	Role          string    `json:"role"` // "RRPP" | "ADMIN" | "MEMBER"
	JoinedAt      time.Time `json:"joinedAt"`
	IsKYCVerified bool      `json:"isKycVerified"`
}
