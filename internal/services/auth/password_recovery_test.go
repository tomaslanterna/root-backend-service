package auth

import (
	"context"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/url"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"testing"
	"time"
)

type recoveryTestRepo struct {
	ports.PasswordRecoveryRepository
	counts       map[string]int
	queued       []*domain.PasswordResetDelivery
	hashes       map[string]string
	used         map[string]bool
	eligible     bool
	passwordHash string
	queueErr     error
	failAttempts int
	completed    bool
}

func (r *recoveryTestRepo) AllowAttempt(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[key]++
	return r.counts[key] <= limit, nil
}
func (r *recoveryTestRepo) QueuePasswordReset(_ context.Context, id, email, hash string, _ time.Duration) error {
	if r.queueErr != nil {
		return r.queueErr
	}
	var userID *string
	if r.eligible {
		id := "user"
		userID = &id
	}
	r.queued = append(r.queued, &domain.PasswordResetDelivery{ID: id, Email: email, UserID: userID, Attempts: 1})
	if r.hashes == nil {
		r.hashes = map[string]string{}
	}
	r.hashes[id] = hash
	return nil
}
func (r *recoveryTestRepo) ClaimPasswordReset(context.Context) (*domain.PasswordResetDelivery, error) {
	if len(r.queued) == 0 {
		return nil, nil
	}
	q := r.queued[0]
	r.queued = r.queued[1:]
	return q, nil
}
func (r *recoveryTestRepo) CompletePasswordResetDelivery(_ context.Context, _ string, sent bool) error {
	r.completed = sent
	return nil
}
func (r *recoveryTestRepo) FailPasswordResetDelivery(_ context.Context, _ string, attempts int) error {
	r.failAttempts = attempts
	return nil
}
func (r *recoveryTestRepo) ResetPassword(_ context.Context, hash, password string) error {
	valid := false
	for _, stored := range r.hashes {
		if hash == stored {
			valid = true
		}
	}
	if !valid || !r.completed || r.used[hash] {
		return domain.ErrInvalidResetToken
	}
	if r.used == nil {
		r.used = map[string]bool{}
	}
	r.used[hash] = true
	r.passwordHash = password
	return nil
}

type recoveryTestSender struct {
	links []string
	err   error
}

func (s *recoveryTestSender) SendPasswordReset(_ context.Context, _ string, link, _ string) error {
	s.links = append(s.links, link)
	return s.err
}
func newRecoveryTest(t *testing.T, eligible bool) (*PasswordRecovery, *recoveryTestRepo, *recoveryTestSender) {
	t.Helper()
	repo := &recoveryTestRepo{eligible: eligible}
	sender := &recoveryTestSender{}
	svc, err := NewPasswordRecovery(repo, sender, strings.Repeat("k", 32), "https://root.example")
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, sender
}
func TestRecoveryQueuesUniformlyAndStoresOnlyTokenHash(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		svc, repo, sender := newRecoveryTest(t, eligible)
		if err := svc.RequestReset(context.Background(), "person@example.com", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
		if len(repo.queued) != 1 || len(sender.links) != 0 {
			t.Fatal("request must queue, not synchronously send or expose membership")
		}
		delivery := repo.queued[0]
		token := svc.deliveryToken(delivery.ID)
		if repo.hashes[delivery.ID] == token || repo.hashes[delivery.ID] != hashResetToken(token) {
			t.Fatal("only token hash may be stored")
		}
		if _, err := svc.ProcessNext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if eligible && len(sender.links) != 1 {
			t.Fatal("eligible account must receive one email")
		}
		if !eligible && len(sender.links) != 0 {
			t.Fatal("absent/OAuth account must not receive reset token")
		}
		if eligible {
			link, err := url.Parse(sender.links[0])
			if err != nil {
				t.Fatal(err)
			}
			if link.Host != "root.example" || link.Path != "/reset-password" || link.RawQuery != "" || link.Fragment != "token="+token {
				t.Fatal("use trusted origin and non-logged fragment")
			}
		}
	}
}
func TestRecoveryLimitsAndValidatesEmail(t *testing.T) {
	svc, _, _ := newRecoveryTest(t, true)
	for _, email := range []string{"", "bad", "Name <person@example.com>", "person@example.com\r\nBcc: attacker@example.com"} {
		if !errors.Is(svc.RequestReset(context.Background(), email, "ip"), domain.ErrInvalidRecoveryEmail) {
			t.Fatal("invalid email accepted")
		}
	}
	for i := 0; i < 3; i++ {
		if err := svc.RequestReset(context.Background(), "PERSON@example.com", "ip"); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(svc.RequestReset(context.Background(), "person@example.com", "another-ip"), domain.ErrAuthRateLimited) {
		t.Fatal("email case must not bypass account rate limit")
	}
	svc, _, _ = newRecoveryTest(t, true)
	for i := 0; i < 10; i++ {
		if err := svc.RequestReset(context.Background(), string(rune('a'+i))+"@example.com", "ip"); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(svc.RequestReset(context.Background(), "extra@example.com", "ip"), domain.ErrAuthRateLimited) {
		t.Fatal("IP limit bypassed")
	}
}
func TestRecoveryDoesNotActivateTokenAfterEmailFailure(t *testing.T) {
	svc, repo, sender := newRecoveryTest(t, true)
	sender.err = errors.New("provider echoed secret-token and private-email")
	if err := svc.RequestReset(context.Background(), "person@example.com", "ip"); err != nil {
		t.Fatal(err)
	}
	token := svc.deliveryToken(repo.queued[0].ID)
	worked, err := svc.ProcessNext(context.Background())
	if !worked || err == nil || strings.Contains(err.Error(), "secret-token") || repo.completed || repo.failAttempts != 1 {
		t.Fatal("failure must be persisted, sanitized, and leave the token inactive")
	}
	if !errors.Is(svc.ResetPassword(context.Background(), token, "NewPassword123", "NewPassword123", "ip"), domain.ErrInvalidResetToken) {
		t.Fatal("undelivered token must not authorize reset")
	}
}
func TestRecoveryUsesBcryptAndSingleUseToken(t *testing.T) {
	svc, repo, _ := newRecoveryTest(t, true)
	if err := svc.RequestReset(context.Background(), "person@example.com", "ip"); err != nil {
		t.Fatal(err)
	}
	token := svc.deliveryToken(repo.queued[0].ID)
	svc.ProcessNext(context.Background())
	for _, p := range []string{"short", strings.Repeat("a", 73), strings.Repeat("é", 37), strings.Repeat(" ", 8)} {
		if !errors.Is(svc.ResetPassword(context.Background(), token, p, p, "ip"), domain.ErrInvalidPassword) {
			t.Fatal("invalid password accepted")
		}
	}
	// Use another IP for attempts; token limit is independently enforced.
	if err := svc.ResetPassword(context.Background(), token, "NewPassword123", "different", "ip"); !errors.Is(err, domain.ErrInvalidPassword) {
		t.Fatal(err)
	}
	if !errors.Is(svc.ResetPassword(context.Background(), token, "NewPassword123", "NewPassword123", "ip"), domain.ErrAuthRateLimited) {
		t.Fatal("token attempt limit bypassed")
	}
	svc, repo, _ = newRecoveryTest(t, true)
	svc.RequestReset(context.Background(), "person@example.com", "ip")
	token = svc.deliveryToken(repo.queued[0].ID)
	svc.ProcessNext(context.Background())
	if err := svc.ResetPassword(context.Background(), token, "NewPassword123", "NewPassword123", "ip"); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.passwordHash), []byte("NewPassword123")) != nil {
		t.Fatal("reset must use existing bcrypt algorithm")
	}
	if !errors.Is(svc.ResetPassword(context.Background(), token, "OtherPassword123", "OtherPassword123", "ip"), domain.ErrInvalidResetToken) {
		t.Fatal("used token accepted")
	}
	if !errors.Is(svc.ResetPassword(context.Background(), "bad", "NewPassword123", "NewPassword123", "ip"), domain.ErrInvalidResetToken) {
		t.Fatal("invalid token accepted")
	}
}
func TestRecoveryConfigurationAndDatabaseFailures(t *testing.T) {
	disabled, err := NewPasswordRecovery(&recoveryTestRepo{}, nil, "", "")
	if err != nil || disabled.Enabled() || !errors.Is(disabled.RequestReset(context.Background(), "a@example.com", "ip"), domain.ErrRecoveryUnavailable) {
		t.Fatal("missing configuration must not claim success")
	}
	for _, base := range []string{"http://root.example", "https://user:pass@root.example", "https://root.example?redirect=evil", "//root.example", "https://root.example/#token=bad"} {
		if _, err := NewPasswordRecovery(&recoveryTestRepo{}, &recoveryTestSender{}, strings.Repeat("k", 32), base); err == nil {
			t.Fatalf("untrusted base URL accepted: %s", base)
		}
	}
	if _, err := NewPasswordRecovery(&recoveryTestRepo{}, &recoveryTestSender{}, "short", "https://root.example"); err == nil {
		t.Fatal("short token key accepted")
	}
	svc, repo, _ := newRecoveryTest(t, true)
	repo.queueErr = errors.New("database unavailable")
	if svc.RequestReset(context.Background(), "a@example.com", "ip") == nil {
		t.Fatal("queue failure ignored")
	}
}
