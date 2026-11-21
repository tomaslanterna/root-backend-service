package domain

import (
	"errors"
	"sort"
	"strings"
)

// MusicGenres son los géneros que se pueden elegir como intereses musicales (mismo orden que en la app).
var MusicGenres = []string{
	"Techno", "House", "Tech House", "Melodic Techno", "Afro House", "Deep House",
	"Minimal", "Progressive", "Trance", "Hard Techno", "Drum & Bass", "Disco",
}

// MaxInterests limita cuántos géneros se guardan por persona.
const MaxInterests = 8

// ErrInvalidInterests: algún género no está en MusicGenres o hay demasiados.
var ErrInvalidInterests = errors.New("invalid music interests")

// NormalizeInterests valida la lista contra MusicGenres (sin importar mayúsculas), saca repetidos y devuelve
// los nombres canónicos en el orden de MusicGenres. Una lista vacía es válida: "no me interesa elegir".
func NormalizeInterests(genres []string) ([]string, error) {
	if len(genres) > MaxInterests {
		return nil, ErrInvalidInterests
	}
	chosen := make(map[string]bool, len(genres))
	for _, raw := range genres {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		found := false
		for _, genre := range MusicGenres {
			if strings.EqualFold(genre, name) {
				chosen[genre] = true
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalidInterests
		}
	}
	out := make([]string, 0, len(chosen))
	for _, genre := range MusicGenres {
		if chosen[genre] {
			out = append(out, genre)
		}
	}
	return out, nil
}

// interestScore puntúa qué tanto coincide un evento con los intereses: el género del evento pesa más que una
// mención en el título o la descripción (hoy muchos eventos no tienen género cargado).
func interestScore(event Event, interests []string) int {
	genre := ""
	if event.Genre != nil {
		genre = strings.ToLower(*event.Genre)
	}
	text := strings.ToLower(event.Title + " " + event.Description)
	score := 0
	for _, interest := range interests {
		term := strings.ToLower(interest)
		if genre != "" && strings.Contains(genre, term) {
			score += 3
		} else if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

// RankByInterests ordena los eventos dejando primero los que más coinciden con los intereses. Entre eventos
// con el mismo puntaje se mantiene el orden original (por fecha). Sin intereses no cambia nada.
func RankByInterests(events []Event, interests []string) []Event {
	if len(interests) == 0 || len(events) < 2 {
		return events
	}
	ranked := make([]Event, len(events))
	copy(ranked, events)
	scores := make(map[string]int, len(ranked))
	for _, event := range ranked {
		scores[event.ID] = interestScore(event, interests)
	}
	sort.SliceStable(ranked, func(i, j int) bool { return scores[ranked[i].ID] > scores[ranked[j].ID] })
	return ranked
}
