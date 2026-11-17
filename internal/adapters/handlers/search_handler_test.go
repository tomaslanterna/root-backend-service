package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"root-backend-service/internal/core/domain"
)

type searchStub struct{ results interface{} }

func (s searchStub) Search(context.Context, string, string, string, string) (interface{}, error) {
	return s.results, nil
}

func searchEvents(t *testing.T, results interface{}) []map[string]interface{} {
	t.Helper()
	handler := NewSearchHandler(searchStub{results}, &bannerS3Stub{})
	req := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewBufferString(`{"query":"x","type":"EVENT"}`))
	rec := httptest.NewRecorder()
	handler.Search(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Results
}

func TestSearchResolvesEventBannerKeysToLoadableURLs(t *testing.T) {
	avatar := "artists/dj.jpg"
	results := searchEvents(t, []domain.Event{
		{ID: "1", Title: "Con flyer", CinematicBannerURL: "events/123_flyer.jpg"},
		{ID: "2", Title: "Ya es una URL", CinematicBannerURL: "https://cdn.example/flyer.webp"},
		{ID: "3", Title: "Sin flyer"},
		{ID: "4", Title: "Con DJ", CinematicBannerURL: "events/9_dj.jpg", Artists: []domain.EventArtist{{Artist: &domain.Artist{Name: "DJ", AvatarURL: &avatar}}}},
	})
	if got := results[0]["cinematicBannerUrl"]; got != "https://signed/events/123_flyer.jpg" {
		t.Errorf("an S3 key must become a signed URL, got %v", got)
	}
	if got := results[1]["cinematicBannerUrl"]; got != "https://cdn.example/flyer.webp" {
		t.Errorf("an existing URL must be left alone, got %v", got)
	}
	if got := results[2]["cinematicBannerUrl"]; got != "" {
		t.Errorf("no flyer stays empty, got %v", got)
	}
	artists := results[3]["artists"].([]interface{})
	artist := artists[0].(map[string]interface{})["artist"].(map[string]interface{})
	if artist["avatarUrl"] != "https://signed/artists/dj.jpg" {
		t.Errorf("artist avatars are resolved too, got %v", artist["avatarUrl"])
	}
}

func TestSearchLeavesOtherResultTypesUntouched(t *testing.T) {
	users := []map[string]interface{}{{"id": "u1", "avatarUrl": "avatars/me.jpg"}}
	results := searchEvents(t, users)
	if results[0]["avatarUrl"] != "avatars/me.jpg" {
		t.Errorf("non-event results are not modified here, got %v", results[0]["avatarUrl"])
	}
}
