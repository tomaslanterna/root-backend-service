package postgres

import (
	"context"
	"database/sql"
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

// Opt-in: crea un esquema aleatorio, copia solo las tablas mínimas que necesita y lo elimina al terminar.
// No toca usuarios, chats ni crews reales.
//
//	TEMP_CREW_TEST_USE_LOCAL_ENV=1 go test ./internal/adapters/repository/postgres -run TestTempCrewPostgresIntegration -v
//
// o definir TEMP_CREW_TEST_DATABASE_URL con una base de pruebas.
func TestTempCrewPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("TEMP_CREW_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("TEMP_CREW_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set TEMP_CREW_TEST_DATABASE_URL (or TEMP_CREW_TEST_USE_LOCAL_ENV=1) to run the PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "temp_crew_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	// Los poolers de transacción no conservan search_path de sesión.
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	params := parsed.Query()
	// `options` lo aplica el servidor en cada conexión del pool (hace falta para probar concurrencia real).
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
		`CREATE TABLE users(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT, username TEXT UNIQUE, avatar_url TEXT,
 is_kyc_verified BOOLEAN DEFAULT FALSE, followers TEXT[] DEFAULT '{}', following TEXT[] DEFAULT '{}')`,
		`CREATE TABLE events(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), title TEXT NOT NULL, date TIMESTAMP NOT NULL,
 end_date TIMESTAMPTZ, location TEXT NOT NULL DEFAULT 'Montevideo', cinematic_banner_url TEXT, created_at TIMESTAMP DEFAULT now())`,
		`CREATE TABLE squads(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_id UUID REFERENCES events(id) ON DELETE CASCADE,
 name VARCHAR NOT NULL, chat_room_id UUID, status VARCHAR NOT NULL DEFAULT 'forming', created_at TIMESTAMP DEFAULT now(),
 expires_at TIMESTAMP DEFAULT (now() + INTERVAL '100 years'), type VARCHAR DEFAULT 'event_match', invite_code VARCHAR UNIQUE,
 is_public BOOLEAN NOT NULL DEFAULT FALSE)`,
		`CREATE TABLE squad_members(squad_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 role VARCHAR DEFAULT 'member', has_ticket BOOLEAN DEFAULT FALSE, joined_at TIMESTAMP DEFAULT now(), PRIMARY KEY(squad_id, user_id))`,
		`CREATE TABLE squad_events(squad_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE, event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 created_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY(squad_id, event_id))`,
		`CREATE TABLE chats(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), type VARCHAR NOT NULL, last_message TEXT, updated_at TIMESTAMP DEFAULT now(), created_at TIMESTAMP DEFAULT now())`,
		`CREATE TABLE chat_participants(chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 joined_at TIMESTAMP DEFAULT now(), PRIMARY KEY(chat_id, user_id))`,
		`CREATE TABLE messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), chat_id UUID REFERENCES chats(id) ON DELETE CASCADE, sender_id UUID REFERENCES users(id),
 content TEXT NOT NULL, type VARCHAR NOT NULL DEFAULT 'text', metadata JSONB, timestamp TIMESTAMP DEFAULT now())`,
		`CREATE TABLE event_rsvps(user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 status VARCHAR CHECK (status IN ('going','not_going')), created_at TIMESTAMP DEFAULT now(), PRIMARY KEY(user_id, event_id))`,
		`CREATE TABLE dance_sessions(id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE, steps_count INTEGER NOT NULL DEFAULT 0, start_time TIMESTAMPTZ, end_time TIMESTAMPTZ,
 is_validated BOOLEAN DEFAULT TRUE, created_at TIMESTAMPTZ DEFAULT now(), UNIQUE(user_id, event_id))`,
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

	r := NewTempCrewRepository(db)
	if err := r.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.InitSchema(ctx); err != nil {
		t.Fatalf("schema must be idempotent: %v", err)
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
	newUser := func(name string) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO users(id, name, username) VALUES($1, $2, $3)`, id, name, strings.ToLower(name)+"_"+id[:6])
		return id
	}
	newEvent := func(title string, hoursFromNow float64) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO events(id, title, date) VALUES($1, $2, NOW() + ($3 * INTERVAL '1 hour'))`, id, title, hoursFromNow)
		return id
	}
	input := func(name string, capacity int) domain.CreateTempCrewInput {
		return domain.CreateTempCrewInput{Name: name, Capacity: capacity, Plan: "Previa a las 22"}
	}
	create := func(host, event, name string, capacity int) string {
		t.Helper()
		id, err := r.Create(ctx, host, event, input(name, capacity))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return id
	}
	request := func(crew, user string) domain.TempCrewJoinRequest {
		t.Helper()
		req, created, err := r.RequestJoin(ctx, crew, user, nil)
		if err != nil || !created {
			t.Fatalf("request join: created=%v err=%v", created, err)
		}
		return req
	}

	sofia, bruno, carla, dario := newUser("Sofia"), newUser("Bruno"), newUser("Carla"), newUser("Dario")
	event, other := newEvent("Fiesta grande", 72), newEvent("Otra fiesta", 96)

	// ---- crear
	crewID := create(sofia, event, "Los del fondo", 3)
	summary, err := r.Summary(ctx, crewID, sofia)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MemberCount != 1 || summary.Capacity != 3 || !summary.IsAdmitting || summary.Host.UserID != sofia ||
		summary.ViewerRole == nil || *summary.ViewerRole != "host" || summary.PendingRequestsCount == nil || *summary.PendingRequestsCount != 0 ||
		len(summary.MembersPreview) != 1 || summary.MembersPreview[0].UserID != sofia || summary.Event.ID != event {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.EventEnded {
		t.Fatal("an upcoming event has not ended")
	}
	if n := count(`SELECT COUNT(*) FROM messages WHERE type='system' AND content LIKE 'Sofia creó la crew para Fiesta grande'`); n != 1 {
		t.Fatalf("expected the creation system message, got %d", n)
	}
	if count(`SELECT COUNT(*) FROM squad_events WHERE squad_id=$1 AND event_id=$2`, crewID, event) != 1 ||
		count(`SELECT COUNT(*) FROM event_rsvps WHERE user_id=$1 AND event_id=$2 AND status='going'`, sofia, event) != 1 ||
		count(`SELECT COUNT(*) FROM chat_participants cp JOIN squads s ON s.chat_room_id=cp.chat_id WHERE s.id=$1`, crewID) != 1 {
		t.Fatal("creation must link the event, mark going and add the host to the chat")
	}
	// Regla: una crew temporal por persona y evento, y el evento tiene que existir y no haber terminado.
	if _, err := r.Create(ctx, sofia, event, input("Otra", 4)); !errors.Is(err, domain.ErrAlreadyInEventCrew) {
		t.Fatalf("second crew for the same event: %v", err)
	}
	if count(`SELECT COUNT(*) FROM squads WHERE type='temporary' AND event_id=$1`, event) != 1 {
		t.Fatal("the failed creation must roll back")
	}
	if _, err := r.Create(ctx, sofia, other, input("En otro evento", 4)); err != nil {
		t.Fatalf("the same person can host in a different event: %v", err)
	}
	if _, err := r.Create(ctx, bruno, uuid.NewString(), input("Fantasma", 4)); !errors.Is(err, domain.ErrEventNotFound) {
		t.Fatalf("missing event: %v", err)
	}
	if _, err := r.Create(ctx, bruno, newEvent("Ya pasó", -40), input("Tarde", 4)); !errors.Is(err, domain.ErrEventEnded) {
		t.Fatalf("ended event: %v", err)
	}

	// ---- solicitudes
	mustExec(`INSERT INTO event_rsvps(user_id, event_id, status) VALUES($1, $2, 'not_going')`, bruno, event)
	// Amigos en común: Sofía (host) sigue a Carla y Carla sigue a Bruno.
	mustExec(`UPDATE users SET following = ARRAY[$1] WHERE id = $2`, carla, sofia)
	mustExec(`UPDATE users SET followers = ARRAY[$1] WHERE id = $2`, carla, bruno)
	req := request(crewID, bruno)
	if req.Status != "pending" || req.User.UserID != bruno || !req.IsGoingToEvent || req.MutualFriendsCount != 1 {
		t.Fatalf("unexpected request: %+v", req)
	}
	if count(`SELECT COUNT(*) FROM event_rsvps WHERE user_id=$1 AND event_id=$2 AND status='going'`, bruno, event) != 1 {
		t.Fatal("asking to join marks the event as going")
	}
	if count(`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='crew.join_request' AND category='crews' AND data->>'crew_id'=$2 AND data->>'request_id'=$3`, sofia, crewID, req.ID) != 1 {
		t.Fatal("the host must be notified")
	}
	again, created, err := r.RequestJoin(ctx, crewID, bruno, nil)
	if err != nil || created || again.ID != req.ID {
		t.Fatalf("a repeated request must be idempotent: %+v created=%v err=%v", again, created, err)
	}
	if _, _, err := r.RequestJoin(ctx, crewID, sofia, nil); !errors.Is(err, domain.ErrAlreadyMember) {
		t.Fatalf("a member cannot request: %v", err)
	}
	if _, _, err := r.RequestJoin(ctx, uuid.NewString(), bruno, nil); !errors.Is(err, domain.ErrTempCrewNotFound) {
		t.Fatalf("unknown crew: %v", err)
	}
	if list, err := r.ListRequests(ctx, crewID, sofia, "pending", 20, 0); err != nil || len(list) != 1 || list[0].ID != req.ID {
		t.Fatalf("host lists requests: %v %v", list, err)
	}
	if _, err := r.ListRequests(ctx, crewID, bruno, "pending", 20, 0); !errors.Is(err, domain.ErrNotTempCrewHost) {
		t.Fatalf("only the host lists requests: %v", err)
	}
	if _, err := r.Decide(ctx, crewID, req.ID, bruno, true); !errors.Is(err, domain.ErrNotTempCrewHost) {
		t.Fatalf("only the host decides: %v", err)
	}
	if _, err := r.SetAdmitting(ctx, crewID, bruno, false); !errors.Is(err, domain.ErrNotTempCrewHost) {
		t.Fatalf("only the host opens or closes: %v", err)
	}
	pendingForHost, _ := r.Summary(ctx, crewID, sofia)
	pendingForGuest, _ := r.Summary(ctx, crewID, bruno)
	if *pendingForHost.PendingRequestsCount != 1 || pendingForGuest.PendingRequestsCount != nil ||
		pendingForGuest.ViewerRequestStatus == nil || *pendingForGuest.ViewerRequestStatus != "pending" || pendingForGuest.ViewerRole != nil {
		t.Fatalf("viewer fields: host=%+v guest=%+v", pendingForHost, pendingForGuest)
	}

	// Bruno también pide entrar a otra crew del mismo evento: al aceptarlo acá, esa se cancela.
	rival := create(dario, event, "Rivales", 4)
	rivalRequest := request(rival, bruno)

	// ---- aceptar
	accepted, err := r.Decide(ctx, crewID, req.ID, sofia, true)
	if err != nil || accepted.Status != "accepted" {
		t.Fatalf("accept: %+v %v", accepted, err)
	}
	if count(`SELECT COUNT(*) FROM squad_members WHERE squad_id=$1`, crewID) != 2 ||
		count(`SELECT COUNT(*) FROM chat_participants cp JOIN squads s ON s.chat_room_id=cp.chat_id WHERE s.id=$1 AND cp.user_id=$2`, crewID, bruno) != 1 ||
		count(`SELECT COUNT(*) FROM messages WHERE type='system' AND content='Sofia aceptó a Bruno'`) != 1 ||
		count(`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='crew.join_request.accepted' AND data->>'crew_id'=$2 AND data ? 'chat_id'`, bruno, crewID) != 1 {
		t.Fatal("accepting must add the member, join the chat, publish the system message and notify")
	}
	var rivalStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM crew_join_requests WHERE id=$1`, rivalRequest.ID).Scan(&rivalStatus); err != nil || rivalStatus != "cancelled" {
		t.Fatalf("the other pending request for the same event must be cancelled: %q %v", rivalStatus, err)
	}
	if _, err := r.Decide(ctx, crewID, req.ID, sofia, true); !errors.Is(err, domain.ErrRequestNotPending) {
		t.Fatalf("deciding twice: %v", err)
	}
	if _, err := r.Decide(ctx, rival, req.ID, dario, true); !errors.Is(err, domain.ErrRequestNotFound) {
		t.Fatalf("a request of another crew is a 404: %v", err)
	}
	if _, _, err := r.RequestJoin(ctx, rival, bruno, nil); !errors.Is(err, domain.ErrAlreadyInEventCrew) {
		t.Fatalf("a member of a crew of the event cannot ask another: %v", err)
	}

	// ---- rechazar
	carlaRequest := request(crewID, carla)
	rejected, err := r.Decide(ctx, crewID, carlaRequest.ID, sofia, false)
	if err != nil || rejected.Status != "rejected" ||
		count(`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='crew.join_request.rejected'`, carla) != 1 ||
		count(`SELECT COUNT(*) FROM squad_members WHERE squad_id=$1`, crewID) != 2 {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if list, _ := r.ListRequests(ctx, crewID, sofia, "rejected", 20, 0); len(list) != 1 {
		t.Fatalf("rejected list: %v", list)
	}
	// Quien fue rechazada puede volver a pedir y cancelar (idempotente).
	retry := request(crewID, carla)
	if err := r.CancelMyRequest(ctx, crewID, carla); err != nil {
		t.Fatal(err)
	}
	if err := r.CancelMyRequest(ctx, crewID, carla); err != nil {
		t.Fatalf("cancel must be idempotent: %v", err)
	}
	if _, err := r.Decide(ctx, crewID, retry.ID, sofia, true); !errors.Is(err, domain.ErrRequestNotPending) {
		t.Fatalf("a cancelled request cannot be accepted: %v", err)
	}

	// ---- cupo: dos aceptaciones simultáneas con un solo lugar libre
	cupo := create(newUser("Host"), newEvent("Evento chico", 50), "Ultimo lugar", 2)
	var cupoHost string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM squad_members WHERE squad_id=$1 AND role='host'`, cupo).Scan(&cupoHost); err != nil {
		t.Fatal(err)
	}
	first, second := request(cupo, newUser("Uno")), request(cupo, newUser("Dos"))
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, results[i] = r.Decide(ctx, cupo, id, cupoHost, true)
		}(i, id)
	}
	wg.Wait()
	wins, full := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrCrewFull):
			full++
		default:
			t.Fatalf("unexpected concurrent accept error: %v", err)
		}
	}
	if wins != 1 || full != 1 || count(`SELECT COUNT(*) FROM squad_members WHERE squad_id=$1`, cupo) != 2 {
		t.Fatalf("capacity must hold: wins=%d full=%d", wins, full)
	}
	if _, _, err := r.RequestJoin(ctx, cupo, newUser("Tres"), nil); !errors.Is(err, domain.ErrNotAdmitting) {
		t.Fatalf("a full crew does not admit requests: %v", err)
	}
	if got, _ := r.Summary(ctx, cupo, cupoHost); got.IsAdmitting {
		t.Fatal("a full crew is not admitting")
	}

	// ---- listado del evento: excluye cerradas y llenas; el total coincide
	listEvent := newEvent("Evento de listas", 60)
	open1, open2 := create(newUser("A"), listEvent, "Abierta grande", 8), create(newUser("B"), listEvent, "Abierta chica", 3)
	closed, full2 := create(newUser("C"), listEvent, "Cerrada", 5), create(newUser("D"), listEvent, "Llena", 2)
	hostOf := func(crew string) string {
		var id string
		if err := db.QueryRowContext(ctx, `SELECT user_id FROM squad_members WHERE squad_id=$1 AND role='host'`, crew).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if _, err := r.SetAdmitting(ctx, closed, hostOf(closed), false); err != nil {
		t.Fatal(err)
	}
	fill := request(full2, newUser("F"))
	if _, err := r.Decide(ctx, full2, fill.ID, hostOf(full2), true); err != nil {
		t.Fatal(err)
	}
	viewer := newUser("Viewer")
	page, err := r.ListForEvent(ctx, listEvent, viewer, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Meta.Total != 2 || len(page.Data) != 2 || page.Data[0].ID != open1 || page.Data[1].ID != open2 || page.Meta.HasMore || page.Meta.ViewerCrewID != nil {
		t.Fatalf("event listing (more free seats first): total=%d data=%+v meta=%+v", page.Meta.Total, page.Data, page.Meta)
	}
	if small, err := r.ListForEvent(ctx, listEvent, viewer, 1, 0); err != nil || len(small.Data) != 1 || !small.Meta.HasMore || small.Meta.Total != 2 {
		t.Fatalf("pagination: %+v %v", small, err)
	}
	// Quien tiene una crew en el evento la recibe como viewerCrewId aunque esté llena.
	if own, err := r.ListForEvent(ctx, listEvent, hostOf(full2), 20, 0); err != nil || own.Meta.ViewerCrewID == nil || *own.Meta.ViewerCrewID != full2 {
		t.Fatalf("viewerCrewId: %+v %v", own.Meta, err)
	}
	if anon, err := r.ListForEvent(ctx, listEvent, "", 20, 0); err != nil || anon.Data[0].ViewerRole != nil || anon.Data[0].ViewerRequestStatus != nil {
		t.Fatalf("anonymous viewer: %v %v", anon, err)
	}
	if _, err := r.ListForEvent(ctx, uuid.NewString(), viewer, 20, 0); !errors.Is(err, domain.ErrEventNotFound) {
		t.Fatalf("unknown event: %v", err)
	}

	// ---- resumen del detalle del evento: lo mismo que /temp-crews?limit=3
	summaryEvent := newEvent("Evento con muchas crews", 80)
	wide := create(newUser("W1"), summaryEvent, "Diez lugares", 10)
	create(newUser("W2"), summaryEvent, "Nueve lugares", 9)
	create(newUser("W3"), summaryEvent, "Ocho lugares", 8)
	create(newUser("W4"), summaryEvent, "Siete lugares", 7)
	crowded := create(newUser("W5"), summaryEvent, "Llena", 2)
	crowdedRequest := request(crowded, newUser("W6"))
	if _, err := r.Decide(ctx, crowded, crowdedRequest.ID, hostOf(crowded), true); err != nil {
		t.Fatal(err)
	}
	for name, who := range map[string]string{"anonymous": "", "stranger": viewer, "host of a full crew": hostOf(crowded), "host of the widest": hostOf(wide)} {
		listed, err := r.ListForEvent(ctx, summaryEvent, who, 3, 0)
		if err != nil {
			t.Fatal(err)
		}
		summarized, err := r.EventSummary(ctx, summaryEvent, who, 3)
		if err != nil {
			t.Fatal(err)
		}
		if summarized.Total != 4 || summarized.Total != listed.Meta.Total || len(summarized.Preview) != 3 || len(summarized.Preview) != len(listed.Data) {
			t.Fatalf("%s: summary %d/%d vs list %d/%d", name, summarized.Total, len(summarized.Preview), listed.Meta.Total, len(listed.Data))
		}
		if (summarized.ViewerCrewID == nil) != (listed.Meta.ViewerCrewID == nil) || (summarized.ViewerCrewID != nil && *summarized.ViewerCrewID != *listed.Meta.ViewerCrewID) {
			t.Fatalf("%s: viewerCrewId differs: %v vs %v", name, summarized.ViewerCrewID, listed.Meta.ViewerCrewID)
		}
		for i := range summarized.Preview {
			a, b := summarized.Preview[i], listed.Data[i]
			if a.ID != b.ID || a.MemberCount != b.MemberCount || a.IsAdmitting != b.IsAdmitting || a.Capacity != b.Capacity ||
				(a.ViewerRole == nil) != (b.ViewerRole == nil) || (a.PendingRequestsCount == nil) != (b.PendingRequestsCount == nil) {
				t.Fatalf("%s: item %d differs: %+v vs %+v", name, i, a, b)
			}
		}
		if summarized.Preview[0].ID != wide {
			t.Fatalf("%s: more free seats first", name)
		}
	}
	if own, _ := r.EventSummary(ctx, summaryEvent, hostOf(crowded), 3); own.ViewerCrewID == nil || *own.ViewerCrewID != crowded {
		t.Fatalf("viewerCrewId points to the viewer's own crew even if it is full: %v", own.ViewerCrewID)
	}
	if beyond, err := r.ListForEvent(ctx, summaryEvent, viewer, 3, 10); err != nil || len(beyond.Data) != 0 || beyond.Meta.Total != 4 || beyond.Meta.HasMore {
		t.Fatalf("a page past the end still reports the total: %+v %v", beyond.Meta, err)
	}
	if empty, err := r.EventSummary(ctx, newEvent("Sin crews", 90), viewer, 3); err != nil || empty.Total != 0 || empty.Preview == nil || len(empty.Preview) != 0 || empty.ViewerCrewID != nil {
		t.Fatalf("an event without crews: %+v %v", empty, err)
	}

	// Los índices alcanzan: con seq scan apagado, ambas consultas tienen un índice que usar.
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
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out.WriteString(line + "\n")
		}
		return out.String()
	}
	if p := plan(`SELECT squad_id FROM squad_members WHERE temp_event_id = $1::uuid AND user_id = $2::uuid`, summaryEvent, viewer); !strings.Contains(p, tempCrewEventUniqueIndex) {
		t.Fatalf("viewer crew lookup should use %s:\n%s", tempCrewEventUniqueIndex, p)
	}
	if p := plan(`SELECT id FROM squads s WHERE s.type = 'temporary' AND s.event_id = $1::uuid`, summaryEvent); !strings.Contains(p, "squads_temporary_event_idx") {
		t.Fatalf("crews of an event should use squads_temporary_event_idx:\n%s", p)
	}

	// ---- quitar el "voy" (DELETE /v1/events/{id}/rsvp)
	events := &EventRepository{db: db}
	leaver := newUser("Leaver")
	mustExec(`INSERT INTO event_rsvps(user_id, event_id, status) VALUES($1,$2,'going')`, leaver, listEvent)
	before := count(`SELECT COUNT(*) FROM event_rsvps WHERE event_id=$1 AND status='going'`, listEvent)
	going, notGoing, err := events.ClearRSVP(ctx, leaver, listEvent)
	if err != nil || going != before-1 || count(`SELECT COUNT(*) FROM event_rsvps WHERE user_id=$1 AND event_id=$2`, leaver, listEvent) != 0 {
		t.Fatalf("clearing the rsvp removes the row and returns fresh counts: %d/%d %v", going, notGoing, err)
	}
	if again, _, err := events.ClearRSVP(ctx, leaver, listEvent); err != nil || again != going {
		t.Fatalf("clearing twice is idempotent: %d %v", again, err)
	}
	if _, _, err := events.ClearRSVP(ctx, leaver, uuid.NewString()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown event: %v", err)
	}
	if _, _, err := events.ClearRSVP(ctx, leaver, "not-a-uuid"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid id is a not-found, not a 500: %v", err)
	}
	if count(`SELECT COUNT(*) FROM event_rsvps WHERE event_id=$1 AND status='going'`, listEvent) != before-1 {
		t.Fatal("other people's rsvps are untouched")
	}

	// ---- mis crews y descubrir
	mine := newUser("Mine")
	mustExec(`INSERT INTO event_rsvps(user_id, event_id, status) VALUES($1,$2,'going')`, mine, listEvent)
	discovered, err := r.Discover(ctx, mine, 20)
	if err != nil || len(discovered) != 2 {
		t.Fatalf("discover only brings admitting crews of events I go to: %v %v", discovered, err)
	}
	if got, _ := r.Discover(ctx, newUser("SinRsvp"), 20); len(got) != 0 {
		t.Fatalf("discover without going: %v", got)
	}
	request(open1, mine)
	discovered, _ = r.Discover(ctx, mine, 20)
	if len(discovered) != 1 || discovered[0].ID != open2 {
		t.Fatalf("a crew with my pending request is hidden from discover: %v", discovered)
	}
	myCrews, err := r.ListMine(ctx, mine)
	if err != nil || len(myCrews) != 1 || myCrews[0].ID != open1 || myCrews[0].ViewerRequestStatus == nil || *myCrews[0].ViewerRequestStatus != "pending" {
		t.Fatalf("my crews include pending requests: %+v %v", myCrews, err)
	}
	if hostCrews, _ := r.ListMine(ctx, sofia); len(hostCrews) != 2 || hostCrews[0].Event.ID != event {
		t.Fatalf("my crews are ordered by event date: %+v", hostCrews)
	}

	// ---- detalle
	if detail, err := r.Detail(ctx, crewID, bruno); err != nil || detail == nil || detail.HostID != sofia || detail.ViewerRole == nil {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	permanent := uuid.NewString()
	mustExec(`INSERT INTO squads(id, name, status, type) VALUES($1, 'Permanente', 'active', 'permanent')`, permanent)
	if detail, err := r.Detail(ctx, permanent, bruno); err != nil || detail != nil {
		t.Fatalf("permanent crews have no temporary detail: %+v %v", detail, err)
	}

	// ---- pasos
	dance := NewDanceRepository(db)
	steps := func(user, event string, count int, start string) {
		t.Helper()
		mustExec(`INSERT INTO dance_sessions(user_id, event_id, steps_count, start_time, end_time) VALUES($1,$2,$3,`+start+`,`+start+` + INTERVAL '1 hour')`, user, event, count)
	}
	stepsOf := func(board []map[string]interface{}) map[string]int {
		out := map[string]int{}
		for _, row := range board {
			out[row["userId"].(string)] = row["steps"].(int)
		}
		return out
	}
	// Crew temporal: solo cuentan los pasos de SU evento, sin importar cuándo se sumó cada persona ni los otros eventos.
	steps(sofia, event, 40, `NOW()`)
	steps(sofia, other, 999, `NOW()`)
	steps(bruno, event, 10, `NOW() - INTERVAL '9 days'`)
	steps(bruno, other, 500, `NOW()`)
	board, err := dance.GetCrewLeaderboardAllTime(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(board); got[sofia] != 40 || got[bruno] != 10 {
		t.Fatalf("a temporary crew counts only its own event: %v", got)
	}
	if byOther, _ := dance.GetCrewLeaderboardByEvent(ctx, crewID, other); stepsOf(byOther)[sofia] != 0 {
		t.Fatalf("asking a temporary crew for another event adds nothing: %v", byOther)
	}

	// Crew normal: solo desde que cada integrante se unió, no todo el histórico.
	ev1, ev2, ev3 := newEvent("Historico", -400), newEvent("Despues de unirse", -48), newEvent("Reciente", -1)
	perm, ana, beto := uuid.NewString(), newUser("Ana"), newUser("Beto")
	mustExec(`INSERT INTO squads(id, name, status, type, created_at) VALUES($1,'Normal','active','permanent', NOW() - INTERVAL '30 days')`, perm)
	mustExec(`INSERT INTO squad_members(squad_id, user_id, role, joined_at) VALUES($1,$2,'host', NOW() - INTERVAL '5 days'), ($1,$3,'member', NOW() - INTERVAL '1 day')`, perm, ana, beto)
	steps(ana, ev1, 100, `NOW() - INTERVAL '20 days'`) // antes de unirse: no cuenta
	steps(ana, ev2, 50, `NOW() - INTERVAL '2 days'`)   // después de unirse: cuenta
	steps(beto, ev1, 70, `NOW() - INTERVAL '2 days'`)  // empezó antes de unirse: no cuenta
	steps(beto, ev3, 30, `NOW() - INTERVAL '1 hour'`)  // después de unirse: cuenta
	board, err = dance.GetCrewLeaderboardAllTime(ctx, perm)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(board); got[ana] != 50 || got[beto] != 30 {
		t.Fatalf("a normal crew counts only since each member joined: %v", got)
	}
	if byEvent, _ := dance.GetCrewLeaderboardByEvent(ctx, perm, ev1); stepsOf(byEvent)[ana] != 0 || stepsOf(byEvent)[beto] != 0 {
		t.Fatalf("the per-event board also ignores what happened before joining: %v", byEvent)
	}
	if byEvent, _ := dance.GetCrewLeaderboardByEvent(ctx, perm, ev2); stepsOf(byEvent)[ana] != 50 {
		t.Fatalf("per-event board since joining: %v", byEvent)
	}
	anaCrews, err := dance.GetUserCrews(ctx, ana)
	if err != nil || len(anaCrews) != 1 || anaCrews[0]["topSteps"] != 50 || anaCrews[0]["totalSteps"] != 80 || anaCrews[0]["userRank"] != 1 {
		t.Fatalf("crew totals use the same rule: %v %v", anaCrews, err)
	}
	// Las temporales no se mezclan en la lista de crews normales.
	if soph, _ := dance.GetUserCrews(ctx, sofia); len(soph) != 0 {
		t.Fatalf("temporary crews are not permanent crews: %v", soph)
	}

	// ---- evento terminado: la crew sigue en la app, pero ya no se puede entrar
	over := newEvent("Se termina", 3)
	overHost, overGuest, overLate := newUser("OverHost"), newUser("OverGuest"), newUser("OverLate")
	overCrew := create(overHost, over, "Hasta el final", 5)
	pendingBeforeEnd := request(overCrew, overGuest)
	other2 := request(overCrew, overLate)
	mustExec(`UPDATE events SET date = NOW() - INTERVAL '30 hours', end_date = NOW() - INTERVAL '1 hour' WHERE id = $1`, over)

	if got, err := r.Summary(ctx, overCrew, overHost); err != nil || !got.EventEnded || got.IsAdmitting {
		t.Fatalf("ended event: still there, not admitting: %+v %v", got, err)
	}
	if detail, err := r.Detail(ctx, overCrew, overHost); err != nil || detail == nil || !detail.EventEnded {
		t.Fatalf("detail keeps working: %+v %v", detail, err)
	}
	if _, _, err := r.RequestJoin(ctx, overCrew, newUser("Tardon"), nil); !errors.Is(err, domain.ErrEventEnded) {
		t.Fatalf("nobody can ask to join after the event: %v", err)
	}
	if _, err := r.Decide(ctx, overCrew, pendingBeforeEnd.ID, overHost, true); !errors.Is(err, domain.ErrEventEnded) {
		t.Fatalf("nobody can be accepted after the event: %v", err)
	}
	if count(`SELECT COUNT(*) FROM squad_members WHERE squad_id=$1`, overCrew) != 1 {
		t.Fatal("no member was added")
	}
	if _, err := r.Decide(ctx, overCrew, other2.ID, overHost, false); err != nil {
		t.Fatalf("rejecting is still possible: %v", err)
	}
	if page, _ := r.ListForEvent(ctx, over, viewer, 20, 0); page.Meta.Total != 0 || len(page.Data) != 0 {
		t.Fatalf("an ended event lists no crews to join: %+v", page)
	}
	// Sigue en "mis crews" y su chat intacto.
	stillMine, err := r.ListMine(ctx, overHost)
	if err != nil || len(stillMine) != 1 || stillMine[0].ID != overCrew || !stillMine[0].EventEnded {
		t.Fatalf("the crew stays in my crews: %+v %v", stillMine, err)
	}
	if count(`SELECT COUNT(*) FROM chat_participants cp JOIN squads s ON s.chat_room_id=cp.chat_id WHERE s.id=$1 AND cp.user_id=$2`, overCrew, overHost) != 1 {
		t.Fatal("the chat stays")
	}
	// El resumen del detalle: evento terminado => sin crews para entrar, pero la crew propia se sigue señalando.
	if ended, err := r.EventSummary(ctx, over, overHost, 3); err != nil || ended.Total != 0 || len(ended.Preview) != 0 || ended.ViewerCrewID == nil || *ended.ViewerCrewID != overCrew {
		t.Fatalf("summary of an ended event: %+v %v", ended, err)
	}
	// El job cancela las pendientes que quedaron y es idempotente.
	if n, err := r.CloseEnded(ctx); err != nil || n != 1 {
		t.Fatalf("close ended: %d %v", n, err)
	}
	if n, err := r.CloseEnded(ctx); err != nil || n != 0 {
		t.Fatalf("close ended must be idempotent: %d %v", n, err)
	}
	var cancelled string
	if err := db.QueryRowContext(ctx, `SELECT status FROM crew_join_requests WHERE id=$1`, pendingBeforeEnd.ID).Scan(&cancelled); err != nil || cancelled != "cancelled" {
		t.Fatalf("pending request is cancelled: %q %v", cancelled, err)
	}
	if count(`SELECT COUNT(*) FROM squads WHERE id=$1`, overCrew) != 1 || count(`SELECT COUNT(*) FROM crew_join_requests WHERE crew_id=$1`, overCrew) != 2 {
		t.Fatal("the crew and its history remain")
	}
}
