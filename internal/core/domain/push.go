package domain

import "errors"

var ErrPushTokenExpired = errors.New("push token expired")

type PushJob struct {
	ID                                                   int64
	Attempt                                              int
	DeviceID, UserID, Token, Platform, MessageID, ChatID string
	SenderName, Content                                  string
	MessageType                                          MessageType
	NotificationID, NotificationType, Title, Body        string
	Category                                             NotificationCategory
	Data                                                 map[string]string
}
