package migrations

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Opt-in: cada caso crea un esquema aleatorio, arma ahí las tablas que en producción se crearon
// a mano (users, events, squads, ...) y lo elimina al terminar. No toca datos reales.
//
//	MIGRATIONS_TEST_DATABASE_URL=postgres://... go test ./internal/adapters/repository/postgres/migrations -v
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MIGRATIONS_TEST_DATABASE_URL to run the PostgreSQL migration tests")
	}
	return dsn
}

// legacyTables son las tablas base que no crea ningún InitSchema: existían antes de que el
// esquema viviera en el código. Solo tienen las columnas que el baseline necesita.
var legacyTables = []string{
	`CREATE TABLE users(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), email TEXT, name TEXT, username TEXT UNIQUE)`,
	`CREATE TABLE events(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), title TEXT NOT NULL DEFAULT 'x', date TIMESTAMP NOT NULL DEFAULT now())`,
	`CREATE TABLE event_rsvps(user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	 status VARCHAR, created_at TIMESTAMP DEFAULT now())`,
	`CREATE TABLE event_swipes(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_id UUID REFERENCES events(id), user_id UUID REFERENCES users(id),
	 has_crew BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMP DEFAULT now())`,
	`CREATE TABLE chats(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), type VARCHAR NOT NULL, last_message TEXT)`,
	`CREATE TABLE chat_participants(chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 PRIMARY KEY(chat_id, user_id))`,
	`CREATE TABLE messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), chat_id UUID REFERENCES chats(id) ON DELETE CASCADE, sender_id UUID REFERENCES users(id),
	 content TEXT NOT NULL, type VARCHAR NOT NULL DEFAULT 'text', timestamp TIMESTAMP DEFAULT now())`,
	`CREATE TABLE squads(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_id UUID REFERENCES events(id) ON DELETE CASCADE, name VARCHAR NOT NULL DEFAULT 'x',
	 chat_room_id UUID, created_at TIMESTAMP DEFAULT now(), expires_at TIMESTAMP NOT NULL DEFAULT now(), type VARCHAR DEFAULT 'event_match')`,
	`CREATE TABLE squad_members(squad_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 PRIMARY KEY(squad_id, user_id))`,
	`CREATE TABLE comments(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), target_id UUID, target_type TEXT, timestamp TIMESTAMP DEFAULT now())`,
	`CREATE TABLE communities(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL, pr_owner_id UUID NOT NULL REFERENCES users(id),
	 country_id TEXT, cover_image_url TEXT, description TEXT, created_at TIMESTAMP DEFAULT now())`,
	`CREATE TABLE community_members(community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 joined_at TIMESTAMP DEFAULT now())`,
	`CREATE TABLE posts(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), community_id UUID REFERENCES communities(id), timestamp TIMESTAMP DEFAULT now())`,
	`CREATE TABLE dance_sessions(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id), event_id UUID NOT NULL REFERENCES events(id),
	 steps_count INTEGER NOT NULL DEFAULT 0, is_validated BOOLEAN DEFAULT TRUE, UNIQUE(user_id, event_id))`,
}

// isolatedDB abre un pool cuyo search_path es un esquema nuevo con las tablas base.
func isolatedDB(t *testing.T, ctx context.Context) (*sql.DB, string) {
	t.Helper()
	dsn := testDSN(t)
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := "migrations_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("isolated schema cleanup: %v", err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	params := parsed.Query()
	params.Set("options", "-c search_path="+schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(6)
	var actual string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actual); err != nil || actual != schema {
		t.Fatalf("refusing to run outside the isolated schema: %q (%v)", actual, err)
	}
	for _, statement := range legacyTables {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	return db, schema
}

// snapshot describe columnas, índices y constraints del esquema, sin la tabla de goose.
func snapshot(t *testing.T, ctx context.Context, db *sql.DB, schema string) string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT 'col ' || table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || COALESCE(column_default, '')
		FROM information_schema.columns WHERE table_schema = $1 AND table_name NOT LIKE 'goose%'
		UNION ALL
		SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename NOT LIKE 'goose%'
		UNION ALL
		SELECT 'con ' || c.conrelid::regclass::text || ' ' || c.conname || ' ' || pg_get_constraintdef(c.oid)
		FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = $1 AND c.conrelid::regclass::text NOT LIKE 'goose%'
		UNION ALL
		SELECT 'trg ' || tgrelid::regclass::text || ' ' || tgname FROM pg_trigger t
		JOIN pg_class r ON r.oid = t.tgrelid JOIN pg_namespace n ON n.oid = r.relnamespace
		WHERE n.nspname = $1 AND NOT t.tgisinternal
		ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func dbVersion(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func latestVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	sources := provider.ListSources()
	return sources[len(sources)-1].Version
}

// Una base como la de producción hoy (con lo que corría el arranque anterior) adopta las
// migraciones sin cambios de esquema salvo event_rrpps, que nunca se creaba al arrancar.
func TestBaselineAdoptsExistingDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, schema := isolatedDB(t, ctx)

	for _, step := range baselineSteps(db) {
		if err := step.run(ctx); err != nil {
			t.Fatalf("legacy startup %s: %v", step.name, err)
		}
	}
	before := snapshot(t, ctx, db, schema)

	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got, want := dbVersion(t, ctx, db), latestVersion(t, db); got != want {
		t.Fatalf("version = %d, want %d", got, want)
	}
	after := snapshot(t, ctx, db, schema)
	var extra []string
	beforeLines := map[string]bool{}
	for _, line := range strings.Split(before, "\n") {
		beforeLines[line] = true
	}
	for _, line := range strings.Split(after, "\n") {
		if !beforeLines[line] {
			extra = append(extra, line)
		}
		delete(beforeLines, line)
	}
	if len(beforeLines) != 0 {
		t.Fatalf("migrations removed schema objects: %v", beforeLines)
	}
	for _, line := range extra {
		if !strings.Contains(line, "event_rrpps") {
			t.Errorf("unexpected schema change on an existing database: %s", line)
		}
	}

	// Un segundo arranque no hace nada.
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if again := snapshot(t, ctx, db, schema); again != after {
		t.Fatal("second Up changed the schema")
	}
}

// Una base que solo tiene las tablas base termina con el mismo esquema que una existente.
func TestFreshDatabaseMatchesExisting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	existing, existingSchema := isolatedDB(t, ctx)
	for _, step := range baselineSteps(existing) {
		if err := step.run(ctx); err != nil {
			t.Fatalf("legacy startup %s: %v", step.name, err)
		}
	}
	if err := Up(ctx, existing); err != nil {
		t.Fatal(err)
	}
	fresh, freshSchema := isolatedDB(t, ctx)
	if err := Up(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(snapshot(t, ctx, existing, existingSchema), existingSchema+".", "")
	got := strings.ReplaceAll(snapshot(t, ctx, fresh, freshSchema), freshSchema+".", "")
	if got != want {
		t.Fatalf("fresh schema differs from migrated existing schema\nfresh:\n%s\n\nexisting:\n%s", got, want)
	}
}

// Varias instancias que arrancan a la vez: una migra y las demás esperan el lock.
func TestConcurrentUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, _ := isolatedDB(t, ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Up(ctx, db)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, want := dbVersion(t, ctx, db), latestVersion(t, db); got != want {
		t.Fatalf("version = %d, want %d", got, want)
	}
}
