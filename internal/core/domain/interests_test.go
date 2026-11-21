package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeInterests(t *testing.T) {
	got, err := NormalizeInterests([]string{"house", " TECHNO ", "House", ""})
	if err != nil || !reflect.DeepEqual(got, []string{"Techno", "House"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := NormalizeInterests(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty list must be valid, got %v %v", got, err)
	}
	if _, err := NormalizeInterests([]string{"Cumbia"}); !errors.Is(err, ErrInvalidInterests) {
		t.Fatalf("unknown genre must fail, got %v", err)
	}
	if _, err := NormalizeInterests(MusicGenres); !errors.Is(err, ErrInvalidInterests) {
		t.Fatalf("more than MaxInterests must fail, got %v", err)
	}
}

func TestRankByInterests(t *testing.T) {
	techno := "Techno"
	events := []Event{
		{ID: "a", Title: "Fiesta de cumbia"},
		{ID: "b", Title: "Noche house en la playa"},
		{ID: "c", Title: "Sin nombre", Genre: &techno},
		{ID: "d", Title: "Otra"},
	}
	ranked := RankByInterests(events, []string{"Techno", "House"})
	ids := []string{}
	for _, e := range ranked {
		ids = append(ids, e.ID)
	}
	// Género cargado (c) pesa más que una mención en el título (b); el resto mantiene su orden.
	if !reflect.DeepEqual(ids, []string{"c", "b", "a", "d"}) {
		t.Fatalf("got %v", ids)
	}
	if events[0].ID != "a" {
		t.Fatal("must not modify the original slice")
	}
	if same := RankByInterests(events, nil); &same[0] != &events[0] {
		t.Fatal("without interests the list is returned as is")
	}
}
