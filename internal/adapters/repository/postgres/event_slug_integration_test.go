package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
)

// Opt-in, como la de afters: crea un esquema aleatorio, corre ahí la migración del slug y lo elimina al terminar.
//
//	AFTERS_TEST_USE_LOCAL_ENV=1 go test ./internal/adapters/repository/postgres -run TestEventSlugPostgresIntegration -v
func TestEventSlugPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("AFTERS_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("AFTERS_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set AFTERS_TEST_DATABASE_URL (or AFTERS_TEST_USE_LOCAL_ENV=1) to run the PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "event_slug_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	mustExec(`CREATE TABLE events(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), title VARCHAR NOT NULL, producer_id UUID, date TIMESTAMP NOT NULL,
 end_date TIMESTAMPTZ, location VARCHAR NOT NULL DEFAULT 'Montevideo', latitude DOUBLE PRECISION, longitude DOUBLE PRECISION,
 cinematic_banner_url TEXT, description TEXT, lineup TEXT[], genre VARCHAR(100), price NUMERIC(10,2), is_featured BOOLEAN,
 created_at TIMESTAMP, ticket_tiers JSONB, ticket_source TEXT, ticket_url TEXT, ticket_info_fetched_at TIMESTAMP,
 geofence_radius_meters INTEGER, department TEXT, country TEXT, city TEXT)`)
	mustExec(`CREATE TABLE event_rsvps(user_id UUID NOT NULL, event_id UUID NOT NULL, status VARCHAR, created_at TIMESTAMP DEFAULT now(), PRIMARY KEY(user_id, event_id))`)

	insert := func(title, date string, createdAt string) string {
		t.Helper()
		var id string
		if err := db.QueryRowContext(ctx, `INSERT INTO events(title, date, created_at) VALUES($1, $2::timestamp, $3::timestamp) RETURNING id::text`,
			title, date, createdAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	slugOf := func(id string) string {
		t.Helper()
		var slug sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT slug FROM events WHERE id = $1::uuid`, id).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		return slug.String
	}

	// Eventos que ya existían antes de la migración: el más viejo se queda con el slug limpio.
	newer := insert("Danzería presenta: GREGGIO", "2026-11-28 23:30", "2026-11-20 10:00")
	older := insert("Danzeria presenta Greggio", "2026-10-03 23:30", "2026-09-01 10:00")
	reserved := insert("Featured", "2026-12-05 23:00", "2026-09-02 10:00")
	empty := insert("¡¡¡ !!!", "2026-12-06 23:00", "2026-09-03 10:00")
	long := insert(strings.Repeat("Noche larga ", 10), "2026-12-07 23:00", "2026-09-04 10:00")

	for i := 0; i < 2; i++ { // idempotente
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range eventSlugSchema() {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("slug schema (run %d): %v", i+1, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	for id, want := range map[string]string{
		older:    "danzeria-presenta-greggio",
		newer:    "danzeria-presenta-greggio-28-nov",
		reserved: "featured-5-dic",
		empty:    "evento",
	} {
		if got := slugOf(id); got != want {
			t.Errorf("backfill slug = %q, want %q", got, want)
		}
	}
	if got := slugOf(long); len(got) > 60 || strings.HasSuffix(got, "-") || !strings.HasPrefix(got, "noche-larga-noche-larga") {
		t.Errorf("long title slug = %q", got)
	}

	// Altas nuevas: el trigger arma el slug. Mismo título y mismo día que uno que ya está: "-2".
	again := insert("Danzería presenta Greggio", "2026-11-28 23:30", "2026-11-25 10:00")
	if got := slugOf(again); got != "danzeria-presenta-greggio-28-nov-2" {
		t.Errorf("repeated title and date slug = %q", got)
	}
	fresh := insert("Ezequiel Arias @ Ñandú Club", "2026-12-12 23:00", "2026-11-25 11:00")
	if got := slugOf(fresh); got != "ezequiel-arias-nandu-club" {
		t.Errorf("new event slug = %q", got)
	}
	// Editar el título no cambia el slug (los links compartidos siguen andando).
	mustExec(`UPDATE events SET title = 'Otro nombre' WHERE id = $1::uuid`, fresh)
	if got := slugOf(fresh); got != "ezequiel-arias-nandu-club" {
		t.Errorf("slug changed after editing the title: %q", got)
	}

	// El detalle se encuentra por UUID o por slug (sin importar mayúsculas).
	repo := &EventRepository{db: db}
	for _, key := range []string{older, strings.ToUpper(older), "danzeria-presenta-greggio", "Danzeria-Presenta-Greggio"} {
		event, err := repo.GetEventByID(ctx, key, "")
		if err != nil || event.ID != older || event.Slug != "danzeria-presenta-greggio" {
			t.Errorf("GetEventByID(%q) = %+v, %v", key, event, err)
		}
	}
	if _, err := repo.GetEventByID(ctx, "no-existe", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown slug: %v", err)
	}
}
