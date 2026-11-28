package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
)

// Opt-in: crea un esquema aleatorio con las tablas mínimas, corre las migraciones de afters y "Mandanos tu evento"
// ahí adentro y lo elimina al terminar. No toca datos reales.
//
//	AFTERS_TEST_USE_LOCAL_ENV=1 go test ./internal/adapters/repository/postgres -run TestAftersPostgresIntegration -v
//
// o definir AFTERS_TEST_DATABASE_URL con una base de pruebas.
func TestAftersPostgresIntegration(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "afters_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	db.SetMaxOpenConns(8)
	var actual string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actual); err != nil || actual != schema {
		t.Fatalf("refusing to run fixtures outside the isolated schema: %q (%v)", actual, err)
	}

	for _, statement := range []string{
		`CREATE TABLE users(id UUID PRIMARY KEY, name TEXT NOT NULL DEFAULT '', username TEXT UNIQUE NOT NULL, avatar_url TEXT,
 is_kyc_verified BOOLEAN DEFAULT FALSE, dob VARCHAR, role VARCHAR NOT NULL DEFAULT 'USER', country VARCHAR(2),
 deleted_at TIMESTAMPTZ, suspended_at TIMESTAMPTZ, followers TEXT[] DEFAULT '{}', following TEXT[] DEFAULT '{}')`,
		`CREATE TABLE events(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), title VARCHAR NOT NULL, producer_id UUID, date TIMESTAMP NOT NULL,
 end_date TIMESTAMPTZ, location VARCHAR NOT NULL DEFAULT 'Montevideo', latitude DOUBLE PRECISION, longitude DOUBLE PRECISION,
 cinematic_banner_url TEXT, description TEXT, lineup TEXT[], genre VARCHAR(100), price NUMERIC(10,2), is_featured BOOLEAN,
 created_at TIMESTAMP, ticket_tiers JSONB, ticket_source TEXT, ticket_url TEXT, ticket_info_fetched_at TIMESTAMP,
 geofence_radius_meters INTEGER, department TEXT, country TEXT, city TEXT)`,
		`CREATE TABLE event_rsvps(user_id UUID NOT NULL, event_id UUID NOT NULL, status VARCHAR, created_at TIMESTAMP DEFAULT now(), PRIMARY KEY(user_id, event_id))`,
		`CREATE TABLE event_rrpps(event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 position SMALLINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(event_id, user_id))`,
		`CREATE TABLE user_blocks(blocker_id UUID NOT NULL, blocked_id UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(blocker_id, blocked_id))`,
		`CREATE TABLE notifications(id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, type TEXT NOT NULL, category TEXT NOT NULL,
 title TEXT NOT NULL, body TEXT NOT NULL, data JSONB NOT NULL DEFAULT '{}', dedupe_key TEXT, scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), read_at TIMESTAMPTZ)`,
		`CREATE UNIQUE INDEX notifications_user_dedupe_uidx ON notifications(user_id, dedupe_key) WHERE dedupe_key IS NOT NULL`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	// Slug de eventos (trigger + backfill), igual que en producción.
	for _, statement := range eventSlugSchema() {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	r := NewAfterRepository(db)
	subs := NewEventSubmissionRepository(db)
	for i := 0; i < 2; i++ {
		if err := r.InitSchema(ctx); err != nil {
			t.Fatalf("afters schema (run %d): %v", i, err)
		}
		if err := subs.InitSchema(ctx); err != nil {
			t.Fatalf("submissions schema (run %d): %v", i, err)
		}
	}
	if ready, err := r.SchemaReady(ctx); err != nil || !ready {
		t.Fatalf("schema ready: %v %v", ready, err)
	}

	mustExec := func(query string, args ...interface{}) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	count := func(query string, args ...interface{}) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	newUser := func(name, role string) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO users(id, name, username, is_kyc_verified, dob, role) VALUES($1, $2, $3, TRUE, '1990-01-01', $4)`,
			id, name, strings.ToLower(name)+"_"+id[:6], role)
		return id
	}
	newEvent := func(title string, hoursFromNow float64, producer interface{}) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO events(id, title, date, producer_id) VALUES($1, $2, (NOW() AT TIME ZONE 'America/Montevideo') + ($3 * INTERVAL '1 hour'), $4)`, id, title, hoursFromNow, producer)
		return id
	}
	now := time.Now().UTC()
	fields := func(name, access string, eventID *string, startsIn time.Duration, capacity *int) domain.AfterFields {
		f := domain.AfterFields{Name: name, EventID: eventID, StartsAt: now.Add(startsIn), Zone: "Pocitos", Address: "Calle secreta 1234",
			Access: access, Capacity: capacity, Genres: []string{"techno"}}
		if access == domain.AfterAccessTicket {
			link := "https://entradas.example/x"
			f.TicketURL = &link
		}
		return f
	}
	create := func(host string, f domain.AfterFields) string {
		t.Helper()
		id, err := r.Create(ctx, host, f, time.Now())
		if err != nil {
			t.Fatalf("create %s: %v", f.Name, err)
		}
		return id
	}
	two := 2

	producer, rrpp, exRRPP, host, guest := newUser("Productora", "USER"), newUser("Rrpp", "RRPP"), newUser("ExRrpp", "USER"), newUser("Host", "USER"), newUser("Guest", "USER")
	event := newEvent("Fiesta grande", 3, producer)
	mustExec(`INSERT INTO event_rrpps(event_id, user_id) VALUES($1, $2), ($1, $3)`, event, rrpp, exRRPP)

	// ---- oficial vs no oficial: en el evento aparecen todos desde que se publican, aunque no haya empezado
	official := create(producer, fields("Oficial", domain.AfterAccessFree, &event, 6*time.Hour, nil))
	byRRPP := create(rrpp, fields("De RRPP", domain.AfterAccessFree, &event, 7*time.Hour, nil))
	byExRRPP := create(exRRPP, fields("De ex RRPP", domain.AfterAccessFree, &event, 4*time.Hour, nil))
	userAfter := create(host, fields("De usuario", domain.AfterAccessApproval, &event, 5*time.Hour, nil))

	events := &EventRepository{db: db}
	EnableAftersEmbed(events)
	detail, err := events.GetEventByID(ctx, event, host)
	if err != nil || detail.Afters == nil {
		t.Fatalf("event detail with afters embed: %+v %v", detail, err)
	}
	if detail.Afters.Total != 4 || len(detail.Afters.Preview) != domain.AfterEventPreview {
		t.Fatalf("every after shows before the event starts (preview capped at 3): %+v", detail.Afters)
	}
	if detail.Afters.Preview[0].ID != official || detail.Afters.Preview[1].ID != byRRPP || detail.Afters.Preview[2].ID != byExRRPP || detail.Afters.Preview[2].IsOfficial {
		t.Fatalf("official first, then by start; a former RRPP is not official: %+v", detail.Afters.Preview)
	}
	if len(detail.Afters.ViewerAfterIDs) != 1 || detail.Afters.ViewerAfterIDs[0] != userAfter {
		t.Fatalf("the host's own after is in viewerAfterIds: %v", detail.Afters.ViewerAfterIDs)
	}
	if anon, err := events.GetEventByID(ctx, event, ""); err != nil || anon.Afters == nil || anon.Afters.Total != 4 || len(anon.Afters.ViewerAfterIDs) != 0 || anon.Afters.Preview[0].ViewerStatus != nil {
		t.Fatalf("anonymous embed: %+v %v", anon, err)
	}
	mustExec(`UPDATE events SET date = (NOW() AT TIME ZONE 'America/Montevideo') - INTERVAL '1 hour' WHERE id = $1`, event)
	detail, _ = events.GetEventByID(ctx, event, guest)
	if detail.Afters.Total != 4 || len(detail.Afters.Preview) != domain.AfterEventPreview {
		t.Fatalf("once the event started every after still shows: %+v", detail.Afters)
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "Calle secreta") {
		t.Fatal("the event embed must never include the address")
	}
	page, err := r.ListForEvent(ctx, event, guest, 2, 2)
	if err != nil || page.Meta.Total != 4 || len(page.Data) != 2 || page.Meta.HasMore || page.Data[1].ID != userAfter {
		t.Fatalf("event listing page 2: %+v %v", page, err)
	}
	noAfters := newEvent("Sin afters", 10, nil)
	if empty, err := events.GetEventByID(ctx, noAfters, guest); err != nil || empty.Afters == nil || empty.Afters.Total != 0 ||
		empty.Afters.Preview == nil || empty.Afters.ViewerAfterIDs == nil {
		t.Fatalf("an event without afters has an empty embed: %+v %v", empty.Afters, err)
	}

	// ---- 3 activos por host (también con pedidos simultáneos)
	limitHost := newUser("Limit", "USER")
	create(limitHost, fields("Uno", domain.AfterAccessFree, nil, time.Hour, nil))
	create(limitHost, fields("Dos", domain.AfterAccessFree, nil, 2*time.Hour, nil))
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = r.Create(ctx, limitHost, fields("Carrera", domain.AfterAccessFree, nil, 3*time.Hour, nil), time.Now())
		}(i)
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) || !(errors.Is(results[0], domain.ErrTooManyActiveAfters) || errors.Is(results[1], domain.ErrTooManyActiveAfters)) {
		t.Fatalf("exactly one concurrent create wins the third slot: %v", results)
	}
	if count(`SELECT COUNT(*) FROM afters WHERE host_user_id = $1`, limitHost) != 3 {
		t.Fatal("the limit holds")
	}
	// Uno que ya terminó deja de contar.
	mustExec(`UPDATE afters SET starts_at = NOW() - INTERVAL '13 hours' WHERE host_user_id = $1 AND name = 'Uno'`, limitHost)
	create(limitHost, fields("Cuatro", domain.AfterAccessFree, nil, 4*time.Hour, nil))

	// ---- cupo: "Voy" simultáneos en un after libre con 2 lugares
	freeAfter := create(host, fields("Libre chico", domain.AfterAccessFree, nil, time.Hour, &two))
	joiners := []string{newUser("J1", "USER"), newUser("J2", "USER"), newUser("J3", "USER")}
	joinErrs := make([]error, len(joiners))
	for i, user := range joiners {
		wg.Add(1)
		go func(i int, user string) {
			defer wg.Done()
			_, joinErrs[i] = r.Join(ctx, freeAfter, user, nil)
		}(i, user)
	}
	wg.Wait()
	wins, full := 0, 0
	for _, err := range joinErrs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrAfterFull):
			full++
		default:
			t.Fatalf("unexpected join error: %v", err)
		}
	}
	if wins != 2 || full != 1 || count(`SELECT COUNT(*) FROM after_attendees WHERE after_id = $1 AND status = 'going'`, freeAfter) != 2 {
		t.Fatalf("capacity must hold: wins=%d full=%d", wins, full)
	}
	if _, err := r.Join(ctx, freeAfter, host, nil); !errors.Is(err, domain.ErrAlreadyJoined) {
		t.Fatalf("the host cannot join: %v", err)
	}

	// ---- aprobación: pedidos, notificaciones y aceptaciones simultáneas con cupo
	approval := create(host, fields("Con aprobación", domain.AfterAccessApproval, nil, 2*time.Hour, &two))
	requesters := []string{newUser("R1", "USER"), newUser("R2", "USER"), newUser("R3", "USER")}
	for _, user := range requesters {
		status, err := r.Join(ctx, approval, user, nil)
		if err != nil || status != domain.AttendeeRequested {
			t.Fatalf("request: %q %v", status, err)
		}
	}
	if _, err := r.Join(ctx, approval, requesters[0], nil); !errors.Is(err, domain.ErrAlreadyJoined) {
		t.Fatalf("requesting twice: %v", err)
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND type = 'after.join_request' AND data->>'id' = $2`, host, approval) != 3 {
		t.Fatal("the host is notified of each request")
	}
	if list, err := r.ListRequests(ctx, approval, host, domain.AttendeeRequested); err != nil || len(list) != 3 {
		t.Fatalf("host lists requests: %v %v", list, err)
	}
	if _, err := r.ListRequests(ctx, approval, guest, domain.AttendeeRequested); !errors.Is(err, domain.ErrNotAfterHost) {
		t.Fatalf("only the host lists requests: %v", err)
	}
	if err := r.Decide(ctx, approval, guest, requesters[0], true); !errors.Is(err, domain.ErrNotAfterHost) {
		t.Fatalf("only the host decides: %v", err)
	}
	// Antes de aceptar, quien pidió no ve la dirección.
	pending, err := r.Detail(ctx, approval, requesters[0])
	if err != nil {
		t.Fatal(err)
	}
	if v := pending.ForViewer(); v.Address != nil || v.AddressVisible || *v.ViewerStatus != domain.AttendeeRequested {
		t.Fatalf("a pending request never gets the address: %+v", v)
	}
	decideErrs := make([]error, len(requesters))
	for i, user := range requesters {
		wg.Add(1)
		go func(i int, user string) {
			defer wg.Done()
			decideErrs[i] = r.Decide(ctx, approval, host, user, true)
		}(i, user)
	}
	wg.Wait()
	wins, full = 0, 0
	var leftOut string
	for i, err := range decideErrs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrAfterFull):
			full++
			leftOut = requesters[i]
		default:
			t.Fatalf("unexpected accept error: %v", err)
		}
	}
	if wins != 2 || full != 1 {
		t.Fatalf("accepting holds the capacity under concurrency: wins=%d full=%d", wins, full)
	}
	if err := r.Decide(ctx, approval, host, leftOut, false); err != nil {
		t.Fatalf("rejecting is still possible: %v", err)
	}
	if err := r.Decide(ctx, approval, host, leftOut, true); !errors.Is(err, domain.ErrAfterRequestDecided) {
		t.Fatalf("deciding twice: %v", err)
	}
	if _, err := r.Join(ctx, approval, leftOut, nil); !errors.Is(err, domain.ErrAlreadyJoined) {
		t.Fatalf("a rejected person cannot ask again: %v", err)
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE type = 'after.join_request.accepted' AND data->>'after_id' = $1`, approval) != 2 ||
		count(`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND type = 'after.join_request.rejected'`, leftOut) != 1 {
		t.Fatal("accept and reject notify the requester")
	}
	var accepted string
	for _, user := range requesters {
		if user != leftOut {
			accepted = user
		}
	}
	row, _ := r.Detail(ctx, approval, accepted)
	if v := row.ForViewer(); v.Address == nil || *v.Address != "Calle secreta 1234" || v.AcceptedCount != 2 {
		t.Fatalf("an accepted person sees the address: %+v", v)
	}

	// ---- aviso: notifica a aceptados y a quienes van, sin la dirección ni el texto
	current, err := r.Current(ctx, approval, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	notice := "Calle secreta 1234, timbre 2"
	if err := r.Update(ctx, approval, host, current.Fields, domain.NoticeChange{Changed: true, Value: &notice}); err != nil {
		t.Fatal(err)
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE type = 'after.notice' AND data->>'after_id' = $1`, approval) != 2 {
		t.Fatal("the notice reaches the two accepted people")
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE (title || body || data::text) LIKE '%Calle secreta%'`) != 0 {
		t.Fatal("no push may carry the address")
	}
	if err := r.Update(ctx, approval, guest, current.Fields, domain.NoticeChange{}); !errors.Is(err, domain.ErrNotAfterHost) {
		t.Fatalf("only the host updates: %v", err)
	}
	one := 1
	lower := current.Fields
	lower.Capacity = &one
	var validation *domain.ValidationError
	if err := r.Update(ctx, approval, host, lower, domain.NoticeChange{}); !errors.As(err, &validation) {
		t.Fatalf("capacity cannot drop below the people in: %v", err)
	}

	// ---- listados: nunca la dirección; filtros por fecha y zona; bloqueos
	listed, err := r.List(ctx, domain.AfterFilter{From: time.Now(), Limit: 50}, guest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(listed)
	if strings.Contains(string(raw), "Calle secreta") || listed.Meta.Total == 0 {
		t.Fatalf("listing: total=%d, never the address", listed.Meta.Total)
	}
	for i := 1; i < len(listed.Data); i++ {
		if listed.Data[i].StartsAt.Before(listed.Data[i-1].StartsAt) {
			t.Fatal("listing is ordered by startsAt")
		}
	}
	until := time.Now().Add(90 * time.Minute)
	soon, _ := r.List(ctx, domain.AfterFilter{From: time.Now(), Until: &until, Limit: 50}, guest)
	for _, item := range soon.Data {
		if item.StartsAt.After(until) {
			t.Fatalf("until filter: %v", item.StartsAt)
		}
	}
	mustExec(`UPDATE afters SET zone = 'Ciudad Vieja' WHERE id = $1`, freeAfter)
	if zoned, _ := r.List(ctx, domain.AfterFilter{From: time.Now(), Zone: "vieja", Limit: 50}, guest); zoned.Meta.Total != 1 || zoned.Data[0].ID != freeAfter {
		t.Fatalf("zone filter: %+v", zoned.Meta)
	}
	blocker := newUser("Blocker", "USER")
	mustExec(`INSERT INTO user_blocks(blocker_id, blocked_id) VALUES($1, $2)`, host, blocker)
	if blocked, _ := r.List(ctx, domain.AfterFilter{From: time.Now(), Zone: "vieja", Limit: 50}, blocker); blocked.Meta.Total != 0 {
		t.Fatal("a blocked person does not see the host's afters")
	}
	if _, err := r.Join(ctx, freeAfter, blocker, nil); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("a blocked person cannot join: %v", err)
	}
	if _, err := r.Detail(ctx, freeAfter, blocker); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("a blocked person cannot see the detail: %v", err)
	}

	// ---- archivado y terminado (otro host: el primero ya tiene 3 activos)
	host2 := newUser("Host2", "USER")
	old := create(host2, fields("Viejo", domain.AfterAccessFree, nil, time.Hour, nil))
	mustExec(`UPDATE afters SET starts_at = NOW() - INTERVAL '30 hours' WHERE id = $1`, old)
	if row, err := r.Detail(ctx, old, host2); err != nil || !row.Archived {
		t.Fatalf("archived after: %+v %v", row, err)
	}
	if _, err := r.Join(ctx, old, guest, nil); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("an archived after is gone: %v", err)
	}
	ended := create(host2, fields("Terminó", domain.AfterAccessFree, nil, time.Hour, nil))
	mustExec(`UPDATE afters SET starts_at = NOW() - INTERVAL '14 hours' WHERE id = $1`, ended)
	if _, err := r.Join(ctx, ended, guest, nil); !errors.Is(err, domain.ErrAfterEnded) {
		t.Fatalf("an ended after: %v", err)
	}

	// ---- denuncias: 3 de usuarios distintos lo ocultan; restore de ADMIN reinicia la cuenta
	reported := create(host2, fields("Denunciado", domain.AfterAccessFree, nil, time.Hour, nil))
	if err := r.Report(ctx, reported, host2, "x"); !errors.Is(err, domain.ErrCannotReportOwnAfter) {
		t.Fatalf("the host cannot report: %v", err)
	}
	reporters := []string{newUser("D1", "USER"), newUser("D2", "USER"), newUser("D3", "USER"), newUser("D4", "USER")}
	for i := 0; i < 2; i++ {
		if err := r.Report(ctx, reported, reporters[i], "spam"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Report(ctx, reported, reporters[0], "spam otra vez"); err != nil {
		t.Fatalf("reporting twice is idempotent: %v", err)
	}
	status := func() string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT status FROM afters WHERE id = $1`, reported).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if status() != domain.AfterStatusActive {
		t.Fatal("two distinct reports (one repeated) do not hide it")
	}
	if err := r.Report(ctx, reported, reporters[2], "spam"); err != nil {
		t.Fatal(err)
	}
	if status() != domain.AfterStatusHidden {
		t.Fatal("three distinct reports hide it")
	}
	if hidden, err := r.AdminList(ctx, domain.AfterStatusHidden, 50, 0); err != nil || len(hidden) != 1 || hidden[0].ID != reported || hidden[0].ReportsCount != 3 {
		t.Fatalf("admin list of hidden: %+v %v", hidden, err)
	}
	if _, err := r.Join(ctx, reported, guest, nil); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("a hidden after cannot be joined: %v", err)
	}
	adminID := newUser("Admin", "ADMIN")
	if err := r.AdminSetStatus(ctx, reported, adminID, domain.AfterStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := r.Report(ctx, reported, reporters[3], "spam"); err != nil || status() != domain.AfterStatusActive {
		t.Fatalf("after a restore only new reports count: %s %v", status(), err)
	}
	if err := r.AdminSetStatus(ctx, uuid.NewString(), adminID, domain.AfterStatusRemoved); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("unknown after: %v", err)
	}

	// ---- borrar y salir
	if err := r.Remove(ctx, freeAfter, guest); !errors.Is(err, domain.ErrNotAfterHost) {
		t.Fatalf("only the host removes: %v", err)
	}
	if err := r.Leave(ctx, approval, accepted); err != nil || count(`SELECT COUNT(*) FROM after_attendees WHERE after_id=$1 AND user_id=$2 AND status='cancelled'`, approval, accepted) != 1 {
		t.Fatalf("leave: %v", err)
	}
	if err := r.Leave(ctx, approval, host); !errors.Is(err, domain.ErrHostCannotLeaveAfter) {
		t.Fatalf("the host cannot leave: %v", err)
	}
	if err := r.Remove(ctx, freeAfter, host); err != nil || r.Remove(ctx, freeAfter, host) != nil {
		t.Fatalf("remove is idempotent: %v", err)
	}
	if _, err := r.Current(ctx, freeAfter, time.Now()); !errors.Is(err, domain.ErrAfterNotFound) {
		t.Fatalf("a removed after cannot be edited: %v", err)
	}

	// ---- índices para las consultas nuevas
	mustExec(`ANALYZE afters`)
	plan := func(query string, args ...interface{}) string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(ctx, `EXPLAIN `+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out strings.Builder
		for rows.Next() {
			var line string
			rows.Scan(&line)
			out.WriteString(line + "\n")
		}
		return out.String()
	}
	// Con la tabla casi vacía el planner puede elegir cualquiera de los dos índices por evento: lo que importa es que
	// la búsqueda por event_id use un índice y no un scan.
	if p := plan(`SELECT id FROM afters a WHERE a.event_id = $1::uuid AND a.status = 'active'`, event); !strings.Contains(p, "Index Cond: (event_id") {
		t.Fatalf("afters of an event should use the partial index:\n%s", p)
	}
	if p := plan(`SELECT id FROM afters a WHERE a.status = 'active' AND a.starts_at <= NOW() ORDER BY a.starts_at`); !strings.Contains(p, "afters_starts_active_idx") {
		t.Fatalf("tonight's afters should use the starts index:\n%s", p)
	}

	// ---- Mandanos tu evento
	submitter := newUser("Submitter", "USER")
	input := func(name string) domain.EventSubmissionInput {
		ticket, lineup := "https://entradas.example/fiesta", "DJ Uno, DJ Dos"
		return domain.EventSubmissionInput{Name: name, StartsAt: now.Add(72 * time.Hour).Truncate(time.Second), VenueName: "Club Central",
			VenueAddress: "Av. Siempreviva 742", FlyerKey: "event-submissions/" + submitter + "/1.jpg", TicketURL: &ticket, Lineup: &lineup,
			Genres: []string{"techno", "house"}, SubmitterRole: domain.SubmitterOrganizer, Contact: "@club", RightsConfirmed: true}
	}
	var firstID string
	for i := 0; i < domain.SubmissionsMaxPending; i++ {
		created, err := subs.Create(ctx, submitter, input("Noche Ácida "+string(rune('A'+i))))
		if err != nil || created.Status != domain.SubmissionPending || created.FlyerURL == nil {
			t.Fatalf("create submission %d: %+v %v", i, created, err)
		}
		if i == 0 {
			firstID = created.ID
		}
	}
	if _, err := subs.Create(ctx, submitter, input("Una más")); !errors.Is(err, domain.ErrTooManyPendingSubmissions) {
		t.Fatalf("pending limit: %v", err)
	}
	mine, err := subs.ListMine(ctx, submitter)
	if err != nil || len(mine) != 5 || mine[0].CreatedAt.Before(mine[4].CreatedAt) {
		t.Fatalf("my submissions, newest first: %v", err)
	}
	pendingList, err := subs.AdminList(ctx, domain.SubmissionPending, 50, 0)
	if err != nil || len(pendingList) != 5 || pendingList[0].Contact != "@club" || pendingList[0].Submitter.UserID != submitter {
		t.Fatalf("admin list: %v", err)
	}
	eventID, err := subs.Publish(ctx, firstID, adminID)
	if err != nil {
		t.Fatal(err)
	}
	var title, location string
	var lineup pq.StringArray
	var startsUTC time.Time
	if err := db.QueryRowContext(ctx, `SELECT title, location, lineup, date AT TIME ZONE 'America/Montevideo' FROM events WHERE id = $1`, eventID).Scan(&title, &location, &lineup, &startsUTC); err != nil {
		t.Fatal(err)
	}
	if title != "Noche Ácida A" || location != "Club Central" || len(lineup) != 2 || !startsUTC.Equal(input("x").StartsAt) {
		t.Fatalf("published event: %q %q %v %v", title, location, lineup, startsUTC)
	}
	if _, err := subs.Publish(ctx, firstID, adminID); !errors.Is(err, domain.ErrSubmissionNotPending) {
		t.Fatalf("publishing twice: %v", err)
	}
	if err := subs.Reject(ctx, mine[0].ID, adminID, "Faltan datos"); err != nil {
		t.Fatal(err)
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND type IN ('event_submission.published', 'event_submission.rejected')`, submitter) != 2 {
		t.Fatal("the submitter is notified of the decision")
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE data::text LIKE '%Siempreviva%'`) != 0 {
		t.Fatal("pushes never include the address")
	}
	if _, err := subs.Create(ctx, submitter, input("Ahora sí")); err != nil {
		t.Fatalf("after two decisions there is room again: %v", err)
	}
	if err := subs.Reject(ctx, uuid.NewString(), adminID, "x"); !errors.Is(err, domain.ErrSubmissionNotFound) {
		t.Fatalf("unknown submission: %v", err)
	}
	date := input("x").StartsAt
	similar, err := subs.Similar(ctx, "noche acida", &date, domain.SimilarEventsMax)
	if err != nil {
		t.Fatal(err)
	}
	_ = similar // "acida" sin tilde no matchea "Ácida" con ILIKE: se prueba con el nombre tal cual.
	similar, err = subs.Similar(ctx, "Noche Ácida", &date, domain.SimilarEventsMax)
	if err != nil || len(similar) != 1 || similar[0].ID != eventID {
		t.Fatalf("similar events: %+v %v", similar, err)
	}
	if far, _ := subs.Similar(ctx, "Noche Ácida", ptrTime(date.Add(10*24*time.Hour)), domain.SimilarEventsMax); len(far) != 0 {
		t.Fatalf("a date far away finds nothing: %v", far)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
