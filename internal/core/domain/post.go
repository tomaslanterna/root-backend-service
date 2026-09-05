package domain

import (
	"time"
)

type Post struct {
	ID             string    `json:"id"`
	AuthorID       string    `json:"authorId"`
	EventID        *string   `json:"eventId,omitempty"`
	CommunityID    *string   `json:"communityId,omitempty"`
	Title          *string   `json:"title,omitempty"`
	Content        string    `json:"content"`
	LongContent    *string   `json:"longContent,omitempty"`
	HeaderImageURL *string   `json:"headerImageUrl"`
	Timestamp      time.Time `json:"timestamp"`
	IsFeatured     bool      `json:"isFeatured"`
	// Campos extra que pueden venir hidratados
	AuthorName     string    `json:"authorName"`
	AuthorAvatar   string    `json:"authorAvatar"`
	IsVerified     bool      `json:"isVerified"`
	LikesCount     int       `json:"likesCount"`
	Tags           []string  `json:"tags"`
}
