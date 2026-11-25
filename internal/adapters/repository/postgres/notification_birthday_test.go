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
	"github.com/joho/godotenv"
	"github.com/lib/pq"
)

// Opt-in: crea un esquema aleatorio con tablas mínimas, lo borra al terminar y
// nunca toca usuarios ni notificaciones reales. No envía push (no hay worker).
//
//	BIRTHDAY_TEST_DATABASE_URL=postgres://... go test ./internal/adapters/repository/postgres -run TestBirthdayNotificationsPostgresIntegration -v
//
// o BIRTHDAY_TEST_USE_LOCAL_ENV=1 para usar DATABASE_URL del .env.
func TestBirthdayNotificationsPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("BIRTHDAY_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("BIRTHDAY_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set BIRTHDAY_TEST_DATABASE_URL (or BIRTHDAY_TEST_USE_LOCAL_ENV=1) to run the PostgreSQL integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "birthday_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	// users.dob es VARCHAR como en producción, para poder cargar fechas inválidas.
	mustExec(`CREATE TABLE users(id UUID PRIMARY KEY,name TEXT,username TEXT,dob VARCHAR(255))`)
	mustExec(`CREATE TABLE messages(id UUID PRIMARY KEY,chat_id UUID NOT NULL,sender_id UUID NOT NULL,content TEXT,type TEXT)`)
	mustExec(`CREATE TABLE chat_participants(chat_id UUID,user_id UUID,PRIMARY KEY(chat_id,user_id))`)
	if err := NewPushRepository(db).InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewNotificationRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}

	newUser := func(username, dob *string) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(`INSERT INTO users(id,name,username,dob) VALUES($1,'Test',$2,$3)`, id, username, dob)
		return id
	}
	str := func(s string) *string { return &s }

	birthday := newUser(str("cumple_hoy"), str("1995-06-15"))
	notToday := newUser(str("otro_dia"), str("1995-06-16"))
	leap := newUser(str("bisiesto"), str("2000-02-29"))
	feb28 := newUser(str("veintiocho"), str("1990-02-28"))
	var silent []string
	for _, dob := range []*string{nil, str(""), str("15/06/1995"), str("1995-6-15"), str("xxxx-06-15"), str("1995-06-15T00:00"), str(" 1995-06-15"), str("1995-13-45")} {
		silent = append(silent, newUser(str("dob_invalida_"+strings.ReplaceAll(uuid.NewString(), "-", "")[:8]), dob))
	}
	for _, username := range []*string{nil, str(""), str("   ")} {
		silent = append(silent, newUser(username, str("1995-06-15")))
	}

	schedule := func(date time.Time) {
		t.Helper()
		if err := repo.ScheduleBirthdays(ctx, date); err != nil {
			t.Fatal(err)
		}
	}
	count := func(userID, dedupe string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='user.birthday' AND dedupe_key=$2`, userID, dedupe).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := repo.ScheduleBirthdays(ctx, time.Time{}); err == nil {
		t.Fatal("zero date must be rejected")
	}

	// Fecha futura fija: scheduled_at debe ser exactamente las 10:00 de Montevideo (13:00 UTC).
	june15 := time.Date(2099, time.June, 15, 0, 0, 0, 0, time.UTC)
	schedule(june15)
	schedule(june15) // idempotente: el worker corre cada minuto y puede reiniciarse
	var (
		total                              int
		typ, category, title, body, dedupe string
		dataMatches                        bool
		scheduledAt                        time.Time
	)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) OVER(),type,category,title,body,data='{"username":"cumple_hoy"}'::jsonb,dedupe_key,scheduled_at
 FROM notifications WHERE user_id=$1`, birthday).Scan(&total, &typ, &category, &title, &body, &dataMatches, &dedupe, &scheduledAt); err != nil {
		t.Fatal(err)
	}
	if total != 1 || typ != "user.birthday" || category != "account" || title != "¡Feliz cumpleaños, @cumple_hoy!" ||
		body != "Desde root te deseamos lo mejor 🎉" || !dataMatches || dedupe != "birthday:2099" {
		t.Fatalf("unexpected birthday notification: total=%d type=%q category=%q title=%q body=%q data=%v dedupe=%q",
			total, typ, category, title, body, dataMatches, dedupe)
	}
	if want := time.Date(2099, time.June, 15, 13, 0, 0, 0, time.UTC); !scheduledAt.Equal(want) {
		t.Fatalf("scheduled_at = %s, want %s", scheduledAt.UTC(), want)
	}
	for _, id := range append([]string{notToday, leap, feb28}, silent...) {
		if n := count(id, "birthday:2099"); n != 0 {
			t.Fatalf("user %s must not be greeted on 15/06: %d", id, n)
		}
	}
	var everything int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&everything); err != nil || everything != 1 {
		t.Fatalf("only the birthday user must be notified: count=%d err=%v", everything, err)
	}

	// 28/02 de un año no bisiesto: también se saluda a quien nació el 29/02.
	schedule(time.Date(2099, time.February, 28, 0, 0, 0, 0, time.UTC))
	if count(leap, "birthday:2099") != 1 || count(feb28, "birthday:2099") != 1 {
		t.Fatalf("28/02 of a common year: leap=%d feb28=%d", count(leap, "birthday:2099"), count(feb28, "birthday:2099"))
	}

	// Año bisiesto: el 29/02 espera a su día.
	schedule(time.Date(2096, time.February, 28, 0, 0, 0, 0, time.UTC))
	if count(leap, "birthday:2096") != 0 || count(feb28, "birthday:2096") != 1 {
		t.Fatalf("28/02 of a leap year: leap=%d feb28=%d", count(leap, "birthday:2096"), count(feb28, "birthday:2096"))
	}
	schedule(time.Date(2096, time.February, 29, 0, 0, 0, 0, time.UTC))
	if count(leap, "birthday:2096") != 1 {
		t.Fatalf("29/02 of a leap year must greet the leap birthday")
	}

	// Si las 10:00 ya pasaron, se agenda para ya (GREATEST(NOW(), ...)).
	schedule(time.Date(2020, time.June, 15, 0, 0, 0, 0, time.UTC))
	var lag float64
	if err := db.QueryRowContext(ctx, `SELECT ABS(EXTRACT(EPOCH FROM scheduled_at-NOW())) FROM notifications WHERE user_id=$1 AND dedupe_key='birthday:2020'`, birthday).Scan(&lag); err != nil || lag > 60 {
		t.Fatalf("past 10:00 must schedule now: lag=%vs err=%v", lag, err)
	}
}
