package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"root-backend-service/internal/core/ports"
)

type resetRepo struct {
	ports.PasswordRecoveryRepository
	err error
}

func (r resetRepo) AllowAttempt(context.Context, string, int, time.Duration) (bool, error) {
	return true, nil
}
func (r resetRepo) ResetPassword(context.Context, string, string) error { return r.err }

type noopSender struct{}

func (noopSender) SendPasswordReset(context.Context, string, string, string) error { return nil }

func TestPasswordResetNotifiesTheSessionCacheOnlyOnSuccess(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for name, repoErr := range map[string]error{"success": nil, "failure": errors.New("db down")} {
		recovery, err := NewPasswordRecovery(resetRepo{err: repoErr}, noopSender{}, strings.Repeat("s", 32), "https://estoesroot.com")
		if err != nil {
			t.Fatal(err)
		}
		called := 0
		recovery.SetOnPasswordReset(func() { called++ })
		err = recovery.ResetPassword(context.Background(), token, "nueva-clave-123", "nueva-clave-123", "1.2.3.4")
		if (repoErr == nil) != (err == nil) {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		if (repoErr == nil && called != 1) || (repoErr != nil && called != 0) {
			t.Fatalf("%s: the cache must be cleared exactly when the password changed (called %d)", name, called)
		}
	}
}
