package domain

import "time"

type EventSurvey struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"userId"`
	EventID            string    `json:"eventId"`
	GeneralRating      int       `json:"generalRating"`
	OrganizationRating *int      `json:"organizationRating,omitempty"`
	VibeRating         *int      `json:"vibeRating,omitempty"`
	SoundVisualRating  *int      `json:"soundVisualRating,omitempty"`
	PricingRating      *int      `json:"pricingRating,omitempty"`
	SpaceRating        *string   `json:"spaceRating,omitempty"` // "crowded", "good", "spacious"
	WouldReturn        *bool     `json:"wouldReturn,omitempty"`
	Comment            *string   `json:"comment,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
}

type SurveyArtistRating struct {
	ID       string `json:"id"`
	SurveyID string `json:"surveyId"`
	ArtistID string `json:"artistId"`
	Rating   int    `json:"rating"`
}
