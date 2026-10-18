package handlers

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type BadgeImages interface {
	OfficialImage(context.Context, string, string) (string, error)
}
type BadgeStorage interface {
	GetObjectLimited(context.Context, string, int64) ([]byte, error)
}
type BadgeMediaHandler struct {
	images  BadgeImages
	storage BadgeStorage
}

func NewBadgeMediaHandler(images BadgeImages, storage BadgeStorage) *BadgeMediaHandler {
	return &BadgeMediaHandler{images: images, storage: storage}
}
func (h *BadgeMediaHandler) Image(w http.ResponseWriter, r *http.Request) {
	event, artist := chi.URLParam(r, "id"), chi.URLParam(r, "artistID")
	if _, err := uuid.Parse(event); err != nil {
		respondWithError(w, 400, "Evento inválido")
		return
	}
	if artist != "" {
		if _, err := uuid.Parse(artist); err != nil {
			respondWithError(w, 400, "Artista inválido")
			return
		}
	}
	source, err := h.images.OfficialImage(r.Context(), event, artist)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, 404, "Sin imagen oficial")
		return
	}
	if err != nil {
		respondWithError(w, 500, "No pudimos consultar la imagen")
		return
	}
	var data []byte
	if strings.HasPrefix(source, "https://") {
		// Only approved public legacy illustrations; never an arbitrary URL proxy.
		parsed, parseErr := url.Parse(source)
		if parseErr != nil || parsed.Host != "images.unsplash.com" || parsed.User != nil {
			respondWithError(w, 422, "Fuente de imagen externa no habilitada")
			return
		}
		req, reqErr := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
		if reqErr != nil {
			respondWithError(w, 422, "Imagen inválida")
			return
		}
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		res, fetchErr := client.Do(req)
		if fetchErr != nil {
			respondWithError(w, 502, "No pudimos cargar la imagen oficial")
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			respondWithError(w, 502, "Imagen oficial no disponible")
			return
		}
		data, err = io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	} else {
		if strings.Contains(source, "..") || (!strings.HasPrefix(source, "events/") && (artist == "" || (!strings.HasPrefix(source, "artists/") && !strings.HasPrefix(source, "avatars/")))) {
			respondWithError(w, 422, "Clave de imagen oficial inválida")
			return
		}
		if h.storage == nil {
			respondWithError(w, 503, "Almacenamiento no disponible")
			return
		}
		data, err = h.storage.GetObjectLimited(r.Context(), source, 8*1024*1024)
	}
	if err != nil {
		respondWithError(w, 502, "No pudimos cargar la imagen oficial")
		return
	}
	contentType := http.DetectContentType(data)
	if len(data) > 8*1024*1024 || (contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp") {
		respondWithError(w, 422, "Formato de imagen no habilitado")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}
