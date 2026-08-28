package domain

import "time"

type Event struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	ProducerID         *string   `json:"producerId,omitempty"`
	Date               time.Time `json:"date"`
	Location           string    `json:"location"`
	CinematicBannerURL string    `json:"cinematicBannerUrl"`
	Description        string    `json:"description"`
	Lineup             []string  `json:"lineup"`
	Genre              *string   `json:"genre,omitempty"`
	Price              *float64  `json:"price,omitempty"`
	IsFree             bool      `json:"isFree"`
	IsFeatured         bool      `json:"isFeatured"`
	GoingCount         int       `json:"goingCount"`
	NotGoingCount      int       `json:"notGoingCount"`
	UserRSVP           *string   `json:"userRsvp,omitempty"` // 'going' | 'not_going' | null
	CreatedAt          time.Time `json:"createdAt"`
}

type Attendee struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Username      string  `json:"username"`
	AvatarURL     *string `json:"avatarUrl"`
	IsKycVerified bool    `json:"isKycVerified"`
}

type EventComment struct {
	ID             string    `json:"id"`
	TargetID       string    `json:"targetId"`
	AuthorID       string    `json:"authorId"`
	AuthorName     string    `json:"authorName"`
	AuthorUsername string    `json:"authorUsername"`
	AuthorAvatar   *string   `json:"authorAvatar"`
	Content        string    `json:"content"`
	Timestamp      time.Time `json:"timestamp"`
}

type EventFilter struct {
	Genre        string
	Location     string
	MinPrice     *float64
	MaxPrice     *float64
	IsFree       *bool
	StartDate    *time.Time
	EndDate      *time.Time
	FeaturedOnly *bool
	Query        string
	Limit        int
	Offset       int
}
