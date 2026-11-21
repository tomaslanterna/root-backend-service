package domain

import "time"

type NotificationCategory string

const (
	NotificationMessages    NotificationCategory = "messages"
	NotificationEvents      NotificationCategory = "events"
	NotificationSocial      NotificationCategory = "social"
	NotificationCrews       NotificationCategory = "crews"
	NotificationCommunities NotificationCategory = "communities"
	NotificationActivity    NotificationCategory = "activity"
	NotificationAccount     NotificationCategory = "account"
)

type Notification struct {
	ID          string               `json:"id"`
	Type        string               `json:"type"`
	Category    NotificationCategory `json:"category"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Data        map[string]string    `json:"data"`
	ReadAt      *time.Time           `json:"readAt,omitempty"`
	ScheduledAt time.Time            `json:"scheduledAt"`
	CreatedAt   time.Time            `json:"createdAt"`
}

type NotificationPage struct {
	Data        []Notification `json:"data"`
	Total       int            `json:"total"`
	UnreadCount int            `json:"unreadCount"`
}

type NotificationPreferences struct {
	Messages    bool `json:"messages"`
	Events      bool `json:"events"`
	Social      bool `json:"social"`
	Crews       bool `json:"crews"`
	Communities bool `json:"communities"`
	Activity    bool `json:"activity"`
	Account     bool `json:"account"`
}

type NotificationInput struct {
	UserID      string
	Type        string
	Category    NotificationCategory
	Title       string
	Body        string
	Data        map[string]string
	DedupeKey   string
	ScheduledAt time.Time
}
