package domain

import (
	"encoding/json"
	"testing"
)

func price(v float64) *float64 { return &v }

func TestCleanTicketTiers(t *testing.T) {
	tiers := []TicketTier{
		{Name: "General - 1er Tanda", Price: price(920)},
		{Name: "CORTESÍA", Price: price(0)},
		{Name: "Cortesia Invitados VIP", Price: price(0)},
		{Name: "CANJE SANTANDER", Price: price(690)},
		{Name: "Canje Itaú Volar", Price: price(500), SoldOut: true},
		{Name: "Backstage", Price: price(3450)},
	}
	clean := CleanTicketTiers(tiers)
	if len(clean) != 4 {
		t.Fatalf("got %d tiers: %+v", len(clean), clean)
	}
	want := []struct {
		name    string
		noPrice bool
	}{{"General - 1er Tanda", false}, {"CANJE SANTANDER", true}, {"Canje Itaú Volar", true}, {"Backstage", false}}
	for i, w := range want {
		if clean[i].Name != w.name || (clean[i].Price == nil) != w.noPrice {
			t.Fatalf("tier %d = %+v, want %+v", i, clean[i], w)
		}
	}
	if !clean[2].SoldOut {
		t.Fatal("the other fields of a canje must be kept")
	}
	// No modifica la lista original.
	if tiers[3].Price == nil {
		t.Fatal("the input was modified")
	}
	// En JSON, un canje viaja con price null.
	raw, _ := json.Marshal(clean[1])
	if string(raw) != `{"name":"CANJE SANTANDER","price":null,"currency":"","soldOut":false,"fewRemaining":false}` {
		t.Fatalf("json = %s", raw)
	}
}

func TestCleanTicketTiersKeepsNilAndEmpty(t *testing.T) {
	if CleanTicketTiers(nil) != nil {
		t.Fatal("nil must stay nil (no ticket info yet)")
	}
	if got := CleanTicketTiers([]TicketTier{{Name: "Cortesía"}}); got == nil || len(got) != 0 {
		t.Fatalf("only courtesies must become an empty list, got %#v", got)
	}
}
