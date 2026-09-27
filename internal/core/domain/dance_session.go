package domain

import (
	"time"
)

type DanceSession struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	EventID     string    `json:"event_id"`
	StepsCount  int       `json:"steps_count"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	IsValidated bool      `json:"is_validated"`
	CreatedAt   time.Time `json:"created_at"`
}
