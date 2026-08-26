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
	IsFeatured         bool      `json:"isFeatured"`
	GoingCount         int       `json:"goingCount"`
	NotGoingCount      int       `json:"notGoingCount"`
	CreatedAt          time.Time `json:"createdAt"`
}
