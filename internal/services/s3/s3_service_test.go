package s3

import (
	"context"
	"testing"
	"time"
)

func TestGenerateViewUrlWithCDN(t *testing.T) {
	svc := &s3ServiceImpl{
		bucketName: "test-bucket",
		cdnBaseURL: "https://d123456.cloudfront.net",
	}

	url, err := svc.GenerateViewUrl(context.Background(), "events/banner.jpg", 1*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://d123456.cloudfront.net/events/banner.jpg"
	if url != expected {
		t.Errorf("expected %q, got %q", expected, url)
	}
}

func TestGenerateViewUrlCacheReusesURL(t *testing.T) {
	svc := &s3ServiceImpl{
		bucketName: "test-bucket",
	}

	fakeURL := "https://test-bucket.s3.amazonaws.com/events/1.jpg?signature=first"
	svc.urlCache.Store("events/1.jpg", cachedURL{
		url:       fakeURL,
		expiresAt: time.Now().Add(5 * 24 * time.Hour),
	})

	url, err := svc.GenerateViewUrl(context.Background(), "events/1.jpg", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if url != fakeURL {
		t.Errorf("expected cached URL %q, got %q", fakeURL, url)
	}
}
