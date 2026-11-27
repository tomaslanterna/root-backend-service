package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Opt-in: crea un esquema aleatorio con las dos tablas mínimas y lo borra al terminar.
//
//	DANCE_TEST_DATABASE_URL=postgres://... go test ./internal/adapters/repository/postgres -run TestSaveDanceSessionCaps -v
func TestSaveDanceSessionCaps(t *testing.T) {
	dsn := os.Getenv("DANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DANCE_TEST_DATABASE_URL to run the PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "dance_cap_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	params := parsed.Query()
	params.Set("options", "-c search_path="+schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE events (id UUID PRIMARY KEY, date TIMESTAMP NOT NULL)`,
		`CREATE TABLE dance_sessions (id UUID PRIMARY KEY, user_id UUID NOT NULL, event_id UUID NOT NULL REFERENCES events(id),
			steps_count INTEGER NOT NULL DEFAULT 0, start_time TIMESTAMPTZ, end_time TIMESTAMPTZ, is_validated BOOLEAN DEFAULT false,
			created_at TIMESTAMPTZ DEFAULT NOW(), UNIQUE (user_id, event_id))`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewDanceRepository(db)
	// Fiesta que empezó hace 10 minutos (la columna guarda hora de Montevideo sin zona).
	startedEvent := uuid.NewString()
	futureEvent := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO events (id, date) VALUES
		($1, (NOW() - INTERVAL '10 minutes') AT TIME ZONE 'America/Montevideo'),
		($2, (NOW() + INTERVAL '2 hours') AT TIME ZONE 'America/Montevideo')`, startedEvent, futureEvent); err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	save := func(eventID string, steps int) int {
		t.Helper()
		err := repo.SaveDanceSession(ctx, &domain.DanceSession{ID: uuid.NewString(), UserID: user, EventID: eventID,
			StepsCount: steps, IsValidated: true, CreatedAt: time.Now(),
			// El cliente podría mandar cualquier hora: el repositorio no la usa.
			StartTime: time.Now().Add(-48 * time.Hour), EndTime: time.Now().Add(48 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		var total int
		if err := db.QueryRowContext(ctx, `SELECT steps_count FROM dance_sessions WHERE user_id=$1 AND event_id=$2`, user, eventID).Scan(&total); err != nil {
			t.Fatal(err)
		}
		return total
	}

	allowed := 10 * ports.MaxDanceStepsPerMinute // ~2400 en 10 minutos
	if got := save(startedEvent, 500); got != 500 {
		t.Fatalf("normal sync: got %d", got)
	}
	if got := save(startedEvent, 1000); got != 1500 {
		t.Fatalf("second sync must add up: got %d", got)
	}
	if got := save(startedEvent, 50000); got < allowed-ports.MaxDanceStepsPerMinute || got > allowed+ports.MaxDanceStepsPerMinute {
		t.Fatalf("rate cap not applied: got %d, want ≈%d", got, allowed)
	}
	before := save(startedEvent, 0)
	// Entre una llamada y otra el tope crece ~4 pasos por segundo: se tolera ese margen.
	if got := save(startedEvent, 1000); got > before+8 {
		t.Fatalf("steps over the cap must be dropped, not added: before %d got %d", before, got)
	}
	if got := save(futureEvent, 300); got != 0 {
		t.Fatalf("steps before the party starts must not count: got %d", got)
	}
	var start time.Time
	if err := db.QueryRowContext(ctx, `SELECT start_time FROM dance_sessions WHERE user_id=$1 AND event_id=$2`, user, startedEvent).Scan(&start); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("start_time must be the party start, got %v ago", d)
	}
}
