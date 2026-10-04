package domain

import "errors"

var ErrPushTokenExpired = errors.New("push token expired")

type PushJob struct {
	ID                                         int64
	Attempt                                    int
	DeviceID, UserID, Token, MessageID, ChatID string
}
