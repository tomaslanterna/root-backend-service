package push

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type MultiPushSender struct {
	fcm  ports.PushSender
	apns ports.PushSender
}

func NewMultiPushSender(fcm, apns ports.PushSender) *MultiPushSender {
	return &MultiPushSender{
		fcm:  fcm,
		apns: apns,
	}
}

func (m *MultiPushSender) Enabled() bool {
	return m != nil && (m.fcm != nil || m.apns != nil)
}

func (m *MultiPushSender) SupportsPlatform(platform string) bool {
	if m == nil {
		return false
	}
	if platform == "ios" {
		return m.apns != nil
	}
	return platform == "android" && m.fcm != nil
}

func (m *MultiPushSender) Send(ctx context.Context, job domain.PushJob) error {
	if job.Platform == "ios" {
		if m.apns == nil {
			return errors.New("APNs sender is not configured for iOS push")
		}
		return m.apns.Send(ctx, job)
	}

	// Default to Android / FCM
	if m.fcm == nil {
		return errors.New("FCM sender is not configured for Android push")
	}
	return m.fcm.Send(ctx, job)
}

var _ ports.PushSender = (*MultiPushSender)(nil)
