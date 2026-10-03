package postgres

import (
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"
)

func TestBuildCommunityWhereIgnoresCaseAndAccents(t *testing.T) {
	where, args := buildCommunityWhere(domain.CommunityFilter{
		Country: "uy", Category: "electronica", Department: "rio negro", Query: "reggaeton",
	}, 1)

	for _, expected := range []string{
		"UPPER(c.country_id) = UPPER($1)",
		"unaccent(LOWER(c.category)) = unaccent(LOWER($2))",
		"unaccent(LOWER(COALESCE(c.zone, ''))) = unaccent(LOWER($3))",
		"unaccent(LOWER(c.name)) LIKE '%' || unaccent(LOWER($4)) || '%'",
	} {
		if !strings.Contains(where, expected) {
			t.Fatalf("expected %q in where clause: %s", expected, where)
		}
	}
	if len(args) != 4 {
		t.Fatalf("expected 4 filter arguments, got %d", len(args))
	}
}
