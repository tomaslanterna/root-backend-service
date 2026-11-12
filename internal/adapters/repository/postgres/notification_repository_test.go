package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
)

// Uses an isolated schema and never touches production tables or rows.
func TestNotificationPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("PUSH_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("PUSH_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set PUSH_TEST_DATABASE_URL to run PostgreSQL integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "notification_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, dropErr := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); dropErr != nil {
			t.Errorf("isolated schema cleanup: %v", dropErr)
		}
	}()

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	var actualSchema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actualSchema); err != nil || actualSchema != schema {
		t.Fatalf("refusing to use a non-isolated schema: %q (%v)", actualSchema, err)
	}

	ddl := []string{
		`CREATE TABLE users(id UUID PRIMARY KEY,name TEXT,username TEXT)`,
		`CREATE TABLE messages(id UUID PRIMARY KEY,chat_id UUID NOT NULL,sender_id UUID NOT NULL,content TEXT,type TEXT)`,
		`CREATE TABLE chat_participants(chat_id UUID,user_id UUID,PRIMARY KEY(chat_id,user_id))`,
		`CREATE TABLE message_receipts(message_id UUID,user_id UUID,read_at TIMESTAMPTZ)`,
		`CREATE TABLE events(id UUID PRIMARY KEY,title TEXT,date TIMESTAMPTZ,end_date TIMESTAMPTZ,location TEXT,producer_id UUID)`,
		`CREATE TABLE event_rsvps(user_id UUID,event_id UUID,status TEXT,PRIMARY KEY(user_id,event_id))`,
		`CREATE TABLE event_attendance(user_id UUID,event_id UUID,departed_at TIMESTAMPTZ,confirmed_at TIMESTAMPTZ,PRIMARY KEY(user_id,event_id))`,
		`CREATE TABLE posts(id UUID PRIMARY KEY,author_id UUID,title TEXT)`,
		`CREATE TABLE communities(id UUID PRIMARY KEY,name TEXT)`,
		`CREATE TABLE community_members(community_id UUID,user_id UUID,PRIMARY KEY(community_id,user_id))`,
	}
	for _, statement := range ddl {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	pushRepo := NewPushRepository(db)
	if err := pushRepo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewNotificationRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("idempotent schema: %v", err)
	}

	actor, recipient := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES($1,'DJ Test','dj_test'),($2,'Fan Test','fan_test')`, actor, recipient); err != nil {
		t.Fatal(err)
	}
	device := uuid.NewString()
	if err := pushRepo.RegisterDevice(ctx, recipient, device, "notification-token-abcdefghijklmnopqrstuvwxyz", "android"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, domain.NotificationInput{
		UserID: recipient, Type: "social.follow", Category: domain.NotificationSocial,
		Title: "Nuevo seguidor", Body: "DJ Test empezó a seguirte.", Data: map[string]string{"username": "dj_test"}, DedupeKey: "follow:" + actor,
	}); err != nil {
		t.Fatal(err)
	}
	page, err := repo.List(ctx, recipient, 30, 0)
	if err != nil || page.Total != 1 || page.UnreadCount != 1 || len(page.Data) != 1 {
		t.Fatalf("unexpected inbox: %+v (%v)", page, err)
	}
	jobs, err := repo.ClaimDeliveries(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].NotificationType != "social.follow" {
		t.Fatalf("unexpected delivery: %+v (%v)", jobs, err)
	}
	prefs := defaultNotificationPreferences()
	prefs.Social = false
	if err := repo.UpdatePreferences(ctx, recipient, prefs); err != nil {
		t.Fatal(err)
	}
	allowed, err := repo.CanSendDelivery(ctx, jobs[0])
	if err != nil || allowed {
		t.Fatalf("disabled social push must not send: allowed=%v err=%v", allowed, err)
	}

	chat, message := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO chat_participants VALUES($1,$2),($1,$3)`, chat, actor, recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages VALUES($1,$2,$3,'hola','text')`, message, chat, actor); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='chat.message'`, recipient).Scan(&page.Total); err != nil || page.Total != 1 {
		t.Fatalf("chat inbox notification missing: count=%d err=%v", page.Total, err)
	}
	var chatJobs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_jobs WHERE user_id=$1 AND message_id=$2`, recipient, message).Scan(&chatJobs); err != nil || chatJobs != 1 {
		t.Fatalf("legacy chat delivery missing: count=%d err=%v", chatJobs, err)
	}

	event := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO events VALUES($1,'Open Park',NOW()+INTERVAL '2 hours',NULL,'Punta del Este',NULL)`, event); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO event_rsvps VALUES($1,$2,'going')`, recipient, event); err != nil {
		t.Fatal(err)
	}
	if err := repo.ScheduleEventReminders(ctx); err != nil {
		t.Fatal(err)
	}
	var reminders int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND type='event.starting_soon'`, recipient).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatalf("event reminder missing: count=%d err=%v", reminders, err)
	}
}
