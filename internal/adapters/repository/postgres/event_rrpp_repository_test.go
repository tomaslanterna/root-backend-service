package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
)

// The migration may only create new objects: never alter, update or delete events or users.
func TestEventRRPPSchemaOnlyCreatesNewObjects(t *testing.T) {
	createOnly := regexp.MustCompile(`^CREATE (TABLE|INDEX) IF NOT EXISTS event_rrpps`)
	forbidden := regexp.MustCompile(`(?i)\b(ALTER|UPDATE|DELETE|DROP|TRUNCATE|INSERT)\b`)
	for _, stmt := range eventRRPPSchema {
		normalized := strings.Join(strings.Fields(stmt), " ")
		if !createOnly.MatchString(normalized) {
			t.Errorf("statement is not an idempotent CREATE of a new object: %s", normalized)
		}
		// ON DELETE CASCADE is a FK action, not a statement.
		if forbidden.MatchString(strings.ReplaceAll(normalized, "ON DELETE CASCADE", "")) {
			t.Errorf("statement must not modify existing data: %s", normalized)
		}
	}
}

// Opt-in: crea un esquema aleatorio con tablas mínimas de users/events y lo elimina al terminar.
// No toca usuarios ni eventos reales.
//
//	EVENT_RRPP_TEST_USE_LOCAL_ENV=1 go test ./internal/adapters/repository/postgres -run TestEventRRPPPostgresIntegration -v
//
// o definir EVENT_RRPP_TEST_DATABASE_URL con una base de pruebas.
func TestEventRRPPPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("EVENT_RRPP_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("EVENT_RRPP_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set EVENT_RRPP_TEST_DATABASE_URL (or EVENT_RRPP_TEST_USE_LOCAL_ENV=1) to run the PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "event_rrpp_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("isolated schema cleanup: %v", err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	params := parsed.Query()
	params.Set("options", "-c search_path="+schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actual string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actual); err != nil || actual != schema {
		t.Fatalf("refusing to run fixtures outside the isolated schema: %q (%v)", actual, err)
	}

	mustExec := func(query string, args ...interface{}) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustExec(`CREATE TABLE users(id UUID PRIMARY KEY, email TEXT, password_hash TEXT, document_id TEXT, name TEXT,
 username TEXT UNIQUE, role VARCHAR(20), avatar_url TEXT, is_kyc_verified BOOLEAN DEFAULT FALSE)`)
	mustExec(`CREATE TABLE events(id UUID PRIMARY KEY, title TEXT NOT NULL, date TIMESTAMP NOT NULL, lineup TEXT[])`)

	newUser := func(name, role string, avatar *string) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO users(id, email, password_hash, document_id, name, username, role, avatar_url, is_kyc_verified)
		 VALUES($1, $2, 'hash', 'DOC-123', $3, $4, $5, $6, TRUE)`, id, id+"@example.test", name, strings.ToLower(strings.ReplaceAll(name, " ", ""))+id[:4], role, avatar)
		return id
	}
	newEvent := func(title string) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO events(id, title, date, lineup) VALUES($1, $2, NOW() + INTERVAL '2 days', ARRAY['DJ Uno'])`, id, title)
		return id
	}
	eventsSnapshot := func() string {
		t.Helper()
		var snapshot sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT string_agg(to_jsonb(e)::text, '|' ORDER BY e.id) FROM events e`).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot.String
	}

	withRRPPs, withoutRRPPs := newEvent("Con RRPPs"), newEvent("Sin RRPPs")
	before := eventsSnapshot()

	repo := NewEventRRPPRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("schema must be idempotent: %v", err)
	}
	if after := eventsSnapshot(); after != before {
		t.Fatalf("the migration must not touch existing events\nbefore: %s\nafter:  %s", before, after)
	}

	avatar := "avatars/x/1.jpg"
	empty := ""
	valentina := newUser("Valentina Sosa", "RRPP", &avatar)
	bruno := newUser("Bruno Diaz", "rrpp", &empty)
	ana := newUser("Ana Perez", "RRPP", nil)
	regular := newUser("Usuario Comun", "USER", nil)

	// Same position: ordered by name (Ana before Bruno). Valentina has a lower position.
	for _, a := range []struct {
		user     string
		position int
	}{{bruno, 2}, {valentina, 1}, {ana, 2}} {
		if err := repo.Assign(ctx, withRRPPs, a.user, a.position); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	if err := repo.Assign(ctx, withRRPPs, regular, 0); !errors.Is(err, domain.ErrNotRRPP) {
		t.Fatalf("non-RRPP users cannot be assigned: %v", err)
	}
	if err := repo.Assign(ctx, withRRPPs, uuid.NewString(), 0); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	if err := repo.Assign(ctx, uuid.NewString(), valentina, 0); !errors.Is(err, domain.ErrEventNotFound) {
		t.Fatalf("missing event: %v", err)
	}

	rrpps, err := repo.ListForEvent(ctx, withRRPPs)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := make([]string, len(rrpps))
	for i, r := range rrpps {
		gotOrder[i] = r.UserID
	}
	if want := []string{valentina, ana, bruno}; strings.Join(gotOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("order must be position ASC then name: got %v want %v", gotOrder, want)
	}
	if rrpps[0].Name != "Valentina Sosa" || rrpps[0].Position != 1 || rrpps[0].AvatarURL == nil || *rrpps[0].AvatarURL != avatar {
		t.Fatalf("unexpected first rrpp: %+v", rrpps[0])
	}
	if rrpps[1].AvatarURL != nil || rrpps[2].AvatarURL != nil {
		t.Fatalf("missing or empty avatars must be null: %+v %+v", rrpps[1], rrpps[2])
	}

	none, err := repo.ListForEvent(ctx, withoutRRPPs)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("an event without rrpps returns an empty non-nil slice: %#v %v", none, err)
	}

	// Re-assigning only moves the position.
	if err := repo.Assign(ctx, withRRPPs, bruno, 0); err != nil {
		t.Fatal(err)
	}
	if rrpps, _ = repo.ListForEvent(ctx, withRRPPs); len(rrpps) != 3 || rrpps[0].UserID != bruno {
		t.Fatalf("upsert must update the position: %+v", rrpps)
	}

	// Losing the RRPP role hides the user without deleting the assignment.
	mustExec(`UPDATE users SET role = 'USER' WHERE id = $1`, ana)
	if rrpps, _ = repo.ListForEvent(ctx, withRRPPs); len(rrpps) != 2 {
		t.Fatalf("users without the RRPP role are not listed: %+v", rrpps)
	}

	if removed, err := repo.Remove(ctx, withRRPPs, valentina); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if removed, err := repo.Remove(ctx, withRRPPs, valentina); err != nil || removed {
		t.Fatalf("second remove must report nothing removed: %v %v", removed, err)
	}

	mustExec(`DELETE FROM events WHERE id = $1`, withRRPPs)
	var left int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_rrpps WHERE event_id = $1`, withRRPPs).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleting the event cascades to event_rrpps: %d %v", left, err)
	}
}
