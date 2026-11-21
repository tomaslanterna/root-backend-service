package domain

import "testing"

func TestIsFullName(t *testing.T) {
	valid := []string{"Juan Pérez", "María José O'Neil", "Ana Pérez-Gómez", "  Lu   Díaz  ", "Zoë Ñúñez"}
	for _, name := range valid {
		if !IsFullName(name) {
			t.Errorf("%q should be a full name", name)
		}
	}
	invalid := []string{"", "Juan", "juanp", "Juan P", "Juan 123", "DJ_Kev Lopez", "  Mathi  "}
	for _, name := range invalid {
		if IsFullName(name) {
			t.Errorf("%q should not be a full name", name)
		}
	}
}

func TestNormalizeFullName(t *testing.T) {
	if got := NormalizeFullName("  Juan   Pérez "); got != "Juan Pérez" {
		t.Fatalf("got %q", got)
	}
}
