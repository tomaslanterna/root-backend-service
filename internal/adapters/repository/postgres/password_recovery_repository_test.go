package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/auth"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryDBUsers struct {
	ports.UserRepository
	db *sql.DB
}

func (r recoveryDBUsers) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	u := &domain.User{Role: "USER"}
	err := r.db.QueryRowContext(ctx, `SELECT id,email,password_hash FROM users WHERE email=$1`, email).Scan(&u.ID, &u.Email, &u.PasswordHash)
	return u, err
}

// Opt-in fixtures live only in a freshly generated schema; no production user is changed.
func TestPasswordRecoveryPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("AUTH_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("AUTH_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set AUTH_TEST_DATABASE_URL or AUTH_TEST_USE_LOCAL_ENV=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("isolated auth fixture cleanup: %v", err)
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
	// Neon may ignore startup search_path; pin one direct connection per worker.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&current); err != nil || current != schema {
		t.Fatalf("refusing auth fixtures outside isolated schema: %q (%v)", current, err)
	}
	for _, ddl := range []string{`CREATE TABLE users(id UUID PRIMARY KEY,email TEXT,password_hash TEXT,updated_at TIMESTAMPTZ DEFAULT NOW())`, `CREATE TABLE push_devices(id UUID PRIMARY KEY,user_id UUID REFERENCES users(id))`} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	r := NewPasswordRecoveryRepository(db)
	for i := 0; i < 2; i++ {
		if err := r.InitSchema(ctx); err != nil {
			t.Fatal(err)
		}
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
	if err := secondDB.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&current); err != nil || current != schema {
		t.Fatal("second worker outside isolated schema")
	}
	workers := []*PasswordRecoveryRepository{r, NewPasswordRecoveryRepository(secondDB)}
	userID := uuid.NewString()
	oldHash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.MinCost)
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,'person@example.com',$2),($3,'google@example.com','')`, userID, string(oldHash), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO push_devices VALUES($1,$2)`, uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	queue := func(email string) string {
		t.Helper()
		id := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
		hash := id
		if err := r.QueuePasswordReset(ctx, id, email, hash, 30*time.Minute); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	for _, email := range []string{"absent@example.com", "google@example.com"} {
		queue(email)
		delivery, err := r.ClaimPasswordReset(ctx)
		if err != nil || delivery == nil || delivery.UserID != nil {
			t.Fatal("ineligible account received a reset token", err)
		}
		if err := r.CompletePasswordResetDelivery(ctx, delivery.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	hash1 := queue("PERSON@example.com")
	delivery, err := r.ClaimPasswordReset(ctx)
	if err != nil || delivery == nil || delivery.UserID == nil || *delivery.UserID != userID {
		t.Fatal("eligible account not claimed", err)
	}
	if err := r.ResetPassword(ctx, hash1, "unused"); !errors.Is(err, domain.ErrInvalidResetToken) {
		t.Fatal("unsent reset token accepted", err)
	}
	if err := r.FailPasswordResetDelivery(ctx, delivery.ID, delivery.Attempts); err != nil {
		t.Fatal(err)
	}
	if next, err := r.ClaimPasswordReset(ctx); err != nil || next != nil {
		t.Fatal("failed delivery retried without backoff", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE password_reset_deliveries SET available_at=NOW() WHERE id=$1`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	delivery, err = r.ClaimPasswordReset(ctx)
	if err != nil || delivery == nil || delivery.Attempts != 2 {
		t.Fatal("retry missing", err)
	}
	if err := r.CompletePasswordResetDelivery(ctx, delivery.ID, true); err != nil {
		t.Fatal(err)
	}
	hash2 := queue("person@example.com")
	second, err := r.ClaimPasswordReset(ctx)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	if err := r.CompletePasswordResetDelivery(ctx, second.ID, true); err != nil {
		t.Fatal(err)
	}
	newHash, _ := bcrypt.GenerateFromPassword([]byte("NewPassword123"), bcrypt.MinCost)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for index, hash := range []string{hash1, hash1, hash2, hash2} {
		worker := workers[index%len(workers)]
		wg.Add(1)
		go func(hash string) {
			defer wg.Done()
			err := worker.ResetPassword(ctx, hash, string(newHash))
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, domain.ErrInvalidResetToken) {
				t.Errorf("concurrent reset: %v", err)
			}
		}(hash)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("expected exactly one atomic reset, got %d", wins.Load())
	}
	version, err := r.GetSessionVersion(ctx, userID)
	if err != nil || version != 1 {
		t.Fatal("reset did not revoke sessions", err)
	}
	var devices int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_devices WHERE user_id=$1`, userID).Scan(&devices); err != nil || devices != 0 {
		t.Fatal("old push registration remains", err)
	}
	svc := auth.NewAuthService(recoveryDBUsers{db: db}, r)
	if _, _, err := svc.Login(ctx, "person@example.com", "OldPassword123"); err == nil {
		t.Fatal("old password still signs in")
	}
	if _, _, err := svc.Login(ctx, "person@example.com", "NewPassword123"); err != nil {
		t.Fatal("persisted new password cannot sign in", err)
	}
	if err := svc.ValidateSession(ctx, userID, 0); !errors.Is(err, domain.ErrInvalidSession) {
		t.Fatal("old session accepted")
	}
	expired := queue("person@example.com")
	item, err := r.ClaimPasswordReset(ctx)
	if err != nil || item == nil {
		t.Fatal(err)
	}
	if err := r.CompletePasswordResetDelivery(ctx, item.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE password_reset_deliveries SET expires_at=NOW()-INTERVAL '1 second' WHERE token_hash=$1`, expired); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{expired, strings.Repeat("0", 64), hash1, hash2} {
		if err := r.ResetPassword(ctx, hash, string(newHash)); !errors.Is(err, domain.ErrInvalidResetToken) {
			t.Fatal("expired, invalid or used token accepted", err)
		}
	}
	var allowed atomic.Int32
	for i := 0; i < 12; i++ {
		worker := workers[i%len(workers)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := worker.AllowAttempt(ctx, strings.Repeat("a", 64), 3, time.Hour)
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 3 {
		t.Fatalf("atomic rate limit allowed %d instead of 3", allowed.Load())
	}
}
