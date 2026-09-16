package domain

import "time"

type Community struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	PROwnerID     string    `json:"prOwnerId"`
	CountryID     string    `json:"countryId"`
	CoverImageURL string    `json:"coverImageUrl"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"createdAt"`
	MembersCount  int       `json:"membersCount"`
	IsMember      bool      `json:"isMember"`
}
