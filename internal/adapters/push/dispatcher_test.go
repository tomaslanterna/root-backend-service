package push

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"testing"
)

type mockSender struct {
	sent []domain.PushJob
	err  error
}

func (m *mockSender) Send(_ context.Context, job domain.PushJob) error {
	m.sent = append(m.sent, job)
	return m.err
}

func TestMultiPushSenderRouting(t *testing.T) {
	fcmMock := &mockSender{}
	apnsMock := &mockSender{}
	dispatcher := NewMultiPushSender(fcmMock, apnsMock)

	if !dispatcher.Enabled() {
		t.Fatal("expected dispatcher to be enabled")
	}

	androidJob := domain.PushJob{Token: "android-tok", Platform: "android"}
	if err := dispatcher.Send(context.Background(), androidJob); err != nil {
		t.Fatalf("unexpected android send error: %v", err)
	}

	iosJob := domain.PushJob{Token: "ios-tok", Platform: "ios"}
	if err := dispatcher.Send(context.Background(), iosJob); err != nil {
		t.Fatalf("unexpected ios send error: %v", err)
	}

	defaultJob := domain.PushJob{Token: "default-tok"}
	if err := dispatcher.Send(context.Background(), defaultJob); err != nil {
		t.Fatalf("unexpected default send error: %v", err)
	}

	if len(fcmMock.sent) != 2 {
		t.Fatalf("expected 2 jobs sent to FCM, got %d", len(fcmMock.sent))
	}
	if len(apnsMock.sent) != 1 {
		t.Fatalf("expected 1 job sent to APNs, got %d", len(apnsMock.sent))
	}
}

func TestMultiPushSenderMissingProvider(t *testing.T) {
	dispatcher := NewMultiPushSender(nil, nil)
	if dispatcher.Enabled() {
		t.Fatal("expected dispatcher without providers to be disabled")
	}

	err := dispatcher.Send(context.Background(), domain.PushJob{Platform: "ios"})
	if err == nil || !errors.Is(err, errors.New("APNs sender is not configured for iOS push")) && err.Error() != "APNs sender is not configured for iOS push" {
		t.Fatalf("expected missing APNs error, got %v", err)
	}

	err = dispatcher.Send(context.Background(), domain.PushJob{Platform: "android"})
	if err == nil || err.Error() != "FCM sender is not configured for Android push" {
		t.Fatalf("expected missing FCM error, got %v", err)
	}
}
