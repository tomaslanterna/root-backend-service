package domain

import "time"

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	PasswordHash  string    `json:"-"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Role          string    `json:"role"`
	AvatarURL     *string   `json:"avatarUrl"`
	Dob           *string   `json:"dob"`
	DocumentID    *string   `json:"documentId"`
	Followers     []string  `json:"followers"`
	Following     []string  `json:"following"`
	IsKycVerified bool      `json:"isKycVerified"`
	Country       *string   `json:"country"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
