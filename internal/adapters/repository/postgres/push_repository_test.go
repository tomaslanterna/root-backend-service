package postgres

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

// Creates an isolated schema. Never modifies real users, devices or conversations.
func TestPushPostgresIntegration(t *testing.T) {
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
	schema := "push_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
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
	// Transaction poolers do not retain session-level search_path parameters.
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
		t.Fatalf("refusing to run fixtures outside the isolated schema: %q (%v)", actualSchema, err)
	}
	ddl := []string{
		`CREATE TABLE users(id UUID PRIMARY KEY,name TEXT,username TEXT)`,
		`CREATE TABLE messages(id UUID PRIMARY KEY,chat_id UUID NOT NULL,sender_id UUID NOT NULL,content TEXT,type TEXT)`,
		`CREATE TABLE chat_participants(chat_id UUID,user_id UUID,PRIMARY KEY(chat_id,user_id))`,
		`CREATE TABLE message_receipts(message_id UUID,user_id UUID,read_at TIMESTAMPTZ)`,
	}
	for _, statement := range ddl {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	r := NewPushRepository(db)
	if err := r.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.InitSchema(ctx); err != nil {
		t.Fatalf("idempotent schema: %v", err)
	}
	a, b, outsider, chat := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, user := range []string{a, b, outsider} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users(id,name,username) VALUES($1,'Emisor de prueba','testuser')`, user); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{a, b} {
		if _, err := db.ExecContext(ctx, `INSERT INTO chat_participants VALUES($1,$2)`, chat, user); err != nil {
			t.Fatal(err)
		}
	}
	deviceA, deviceB, deviceO := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, device := range []struct{ user, id, token string }{{a, deviceA, "token-a-abcdefghijklmnopqrstuvwxyz"}, {b, deviceB, "token-b-abcdefghijklmnopqrstuvwxyz"}, {outsider, deviceO, "token-o-abcdefghijklmnopqrstuvwxyz"}} {
		if err := r.RegisterDevice(ctx, device.user, device.id, device.token); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO messages VALUES($1,$2,$3,'private content','text') ON CONFLICT DO NOTHING`, id, chat, a); err != nil {
			t.Fatal(err)
		}
	}
	message := uuid.NewString()
	insert(message)
	insert(message)
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("fanout/idempotency: count=%d err=%v", count, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages VALUES($1,$2,$3,'rolled back','text')`, uuid.NewString(), chat, a); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback left jobs: %d %v", count, err)
	}
	secondDB, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	secondDB.SetMaxOpenConns(1)
	secondDB.SetMaxIdleConns(1)
	if _, err := secondDB.ExecContext(ctx, `SET search_path TO `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	if err := secondDB.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actualSchema); err != nil || actualSchema != schema {
		t.Fatal("second connection must use isolated schema")
	}
	workers := []*PushRepository{r, NewPushRepository(secondDB)}
	// Two workers cannot claim the same pending row.
	var wg sync.WaitGroup
	claims := make(chan []domain.PushJob, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(worker *PushRepository) {
			defer wg.Done()
			jobs, err := worker.ClaimJobs(ctx)
			if err != nil {
				t.Error(err)
			}
			claims <- jobs
		}(workers[i])
	}
	wg.Wait()
	close(claims)
	var jobs []domain.PushJob
	for batch := range claims {
		jobs = append(jobs, batch...)
	}
	if len(jobs) != 1 || jobs[0].UserID != b {
		t.Fatalf("expected only recipient device, got %d", len(jobs))
	}
	job := jobs[0]
	if job.SenderName != "Emisor de prueba" || job.Content != "private content" || job.MessageType != domain.MessageTypeText {
		t.Fatal("claimed job must include the sender and message preview")
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET name=' ',username='sender_alias' WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	for _, preview := range []struct{ content, kind string }{
		{strings.Repeat("🎉", 300), "text"},
		{"https://private.example/photo", "image"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO messages VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), chat, a, preview.content, preview.kind); err != nil {
			t.Fatal(err)
		}
	}
	previews, err := r.ClaimJobs(ctx)
	if err != nil || len(previews) != 2 {
		t.Fatalf("expected text and photo preview jobs: %d %v", len(previews), err)
	}
	for _, preview := range previews {
		if preview.SenderName != "sender_alias" {
			t.Fatal("blank display name must fall back to username")
		}
		if preview.MessageType == domain.MessageTypeText && preview.Content != strings.Repeat("🎉", 241) {
			t.Fatal("SQL must bound text previews without breaking Unicode")
		}
		if preview.MessageType == domain.MessageTypeImage && preview.Content != "" {
			t.Fatal("SQL must not fetch private image URLs for push previews")
		}
	}
	if allowed, err := r.CanSend(ctx, job); err != nil || !allowed {
		t.Fatalf("eligible: %v %v", allowed, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO message_receipts VALUES($1,$2,NOW())`, message, b); err != nil {
		t.Fatal(err)
	}
	if allowed, err := r.CanSend(ctx, job); err != nil || allowed {
		t.Fatalf("read must suppress: %v %v", allowed, err)
	}
	if err := r.FinishJob(ctx, job, false, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterDevice(ctx, outsider, deviceB, "rotated-b-abcdefghijklmnopqrstuvwxyz"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := r.CanSend(ctx, job); err != nil || allowed {
		t.Fatalf("old account must not receive: %v %v", allowed, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old-account jobs removed: %d %v", count, err)
	}
	// A user cannot delete another account's installation.
	if err := r.RemoveDevice(ctx, b, deviceB); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_devices WHERE id=$1`, deviceB).Scan(&count); err != nil || count != 1 {
		t.Fatal("cross-account removal")
	}
	// A late invalid-token response must not delete a newly rotated token.
	if err := r.ExpireToken(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_devices WHERE id=$1`, deviceB).Scan(&count); err != nil || count != 1 {
		t.Fatal("new token incorrectly expired")
	}
	if err := r.RemoveDevice(ctx, outsider, deviceB); err != nil {
		t.Fatal(err)
	}
}
