package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"image"
	"image/png"
	"net/http/httptest"
	"testing"
)

type imageFake struct {
	source, event, artist string
	err                   error
	data                  []byte
	key                   string
}

func (f *imageFake) OfficialImage(_ context.Context, event, artist string) (string, error) {
	f.event, f.artist = event, artist
	return f.source, f.err
}
func (f *imageFake) GetObjectLimited(_ context.Context, key string, _ int64) ([]byte, error) {
	f.key = key
	return f.data, nil
}
func TestOfficialBadgeMedia(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	event, artist := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name, source string
		err          error
		want         int
	}{
		{"stored flyer", "events/flyer.png", nil, 200}, {"missing", "", sql.ErrNoRows, 404}, {"repository failure", "", sql.ErrConnDone, 500},
		{"SSRF", "https://127.0.0.1/admin", nil, 422}, {"credentials URL", "https://evil@images.unsplash.com/x", nil, 422},
		{"KYC key", "kyc/private.jpg", nil, 422}, {"traversal", "events/../secret", nil, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &imageFake{source: tc.source, err: tc.err, data: buffer.Bytes()}
			h := NewBadgeMediaHandler(fake, fake)
			r := chi.NewRouter()
			r.Get("/{id}", h.Image)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/"+event, nil))
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.want == 200 && (fake.key != tc.source || w.Header().Get("Content-Type") != "image/png") {
				t.Fatal("wrong image")
			}
		})
	}
	fake := &imageFake{source: "artists/portrait.png", data: buffer.Bytes()}
	r := chi.NewRouter()
	r.Get("/{id}/{artistID}", NewBadgeMediaHandler(fake, fake).Image)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/"+event+"/"+artist, nil))
	if w.Code != 200 || fake.artist != artist {
		t.Fatal("artist must be tied to requested event")
	}
}
