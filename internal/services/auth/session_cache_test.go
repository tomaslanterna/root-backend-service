package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
)

type fakeSessions struct {
	mu       sync.Mutex
	versions map[string]int64
	cached   atomic.Int64 // lecturas con GetSessionVersion
	loginHit atomic.Int64
}

func newFakeSessions(versions map[string]int64) *fakeSessions {
	return &fakeSessions{versions: versions}
}

func (f *fakeSessions) GetSessionVersion(_ context.Context, userID string) (int64, error) {
	f.cached.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[userID]
	if !ok {
		return 0, errors.New("user not found")
	}
	return v, nil
}

func (f *fakeSessions) GetPasswordAndSessionVersion(_ context.Context, userID string) (string, int64, error) {
	f.loginHit.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[userID]
	if !ok {
		return "", 0, errors.New("user not found")
	}
	return "hash", v, nil
}

func (f *fakeSessions) bump(userID string) {
	f.mu.Lock()
	f.versions[userID]++
	f.mu.Unlock()
}

func TestSessionCacheAvoidsRepeatedQueries(t *testing.T) {
	inner := newFakeSessions(map[string]int64{"u1": 3})
	cache := NewSessionVersionCache(inner, time.Minute, 100)
	for i := 0; i < 50; i++ {
		if v, err := cache.GetSessionVersion(context.Background(), "u1"); err != nil || v != 3 {
			t.Fatalf("version %d err %v", v, err)
		}
	}
	if inner.cached.Load() != 1 {
		t.Fatalf("50 requests must cost one query, got %d", inner.cached.Load())
	}
}

func TestSessionCacheExpires(t *testing.T) {
	inner := newFakeSessions(map[string]int64{"u1": 1})
	cache := NewSessionVersionCache(inner, 10*time.Second, 100)
	now := time.Now()
	cache.now = func() time.Time { return now }
	cache.GetSessionVersion(context.Background(), "u1")
	now = now.Add(9 * time.Second)
	cache.GetSessionVersion(context.Background(), "u1")
	if inner.cached.Load() != 1 {
		t.Fatalf("still fresh at 9s: %d queries", inner.cached.Load())
	}
	now = now.Add(2 * time.Second)
	cache.GetSessionVersion(context.Background(), "u1")
	if inner.cached.Load() != 2 {
		t.Fatalf("must re-query after the ttl: %d queries", inner.cached.Load())
	}
}

func TestSessionCacheDisabledAlwaysQueries(t *testing.T) {
	inner := newFakeSessions(map[string]int64{"u1": 1})
	cache := NewSessionVersionCache(inner, 0, 100)
	for i := 0; i < 5; i++ {
		cache.GetSessionVersion(context.Background(), "u1")
	}
	if inner.cached.Load() != 5 {
		t.Fatalf("ttl 0 disables the cache: %d queries", inner.cached.Load())
	}
}

func TestSessionCacheNeverCachesFailures(t *testing.T) {
	inner := newFakeSessions(map[string]int64{})
	cache := NewSessionVersionCache(inner, time.Minute, 100)
	for i := 0; i < 3; i++ {
		if _, err := cache.GetSessionVersion(context.Background(), "ghost"); err == nil {
			t.Fatal("an unknown user is an error")
		}
	}
	if inner.cached.Load() != 3 {
		t.Fatalf("errors are not cached: %d queries", inner.cached.Load())
	}
	// Un usuario borrado deja de validar en cuanto vence el caché de su última lectura, y el error lo olvida.
	inner.versions["u2"] = 1
	cache.GetSessionVersion(context.Background(), "u2")
	delete(inner.versions, "u2")
	cache.Invalidate("u2")
	if _, err := cache.GetSessionVersion(context.Background(), "u2"); err == nil {
		t.Fatal("after invalidation the deleted user must be rejected")
	}
}

func TestSessionCacheIsBounded(t *testing.T) {
	inner := newFakeSessions(map[string]int64{})
	cache := NewSessionVersionCache(inner, time.Minute, 10)
	for i := 0; i < 100; i++ {
		id := string(rune('a'+i%26)) + string(rune('A'+i/26))
		inner.versions[id] = 1
		cache.GetSessionVersion(context.Background(), id)
	}
	cache.mu.RLock()
	size := len(cache.entries)
	cache.mu.RUnlock()
	if size > 10 {
		t.Fatalf("memory must stay bounded, got %d entries", size)
	}
}

func TestSessionCacheLoginRefreshesTheEntry(t *testing.T) {
	inner := newFakeSessions(map[string]int64{"u1": 4})
	cache := NewSessionVersionCache(inner, time.Minute, 100)
	if _, v, err := cache.GetPasswordAndSessionVersion(context.Background(), "u1"); err != nil || v != 4 {
		t.Fatalf("login read: %d %v", v, err)
	}
	cache.GetSessionVersion(context.Background(), "u1")
	if inner.cached.Load() != 0 || inner.loginHit.Load() != 1 {
		t.Fatalf("the login read primes the cache: session reads=%d login reads=%d", inner.cached.Load(), inner.loginHit.Load())
	}
}

func TestValidateSessionWithCache(t *testing.T) {
	ctx := context.Background()
	inner := newFakeSessions(map[string]int64{"u1": 0})
	cache := NewSessionVersionCache(inner, time.Minute, 100)
	svc := NewAuthService(nil, cache)

	if err := svc.ValidateSession(ctx, "u1", 0); err != nil {
		t.Fatalf("valid session: %v", err)
	}
	if err := svc.ValidateSession(ctx, "u1", 0); err != nil || inner.cached.Load() != 1 {
		t.Fatalf("second request is served from the cache: %v / %d queries", err, inner.cached.Load())
	}

	// Cambio de contraseña: sube la versión. El token nuevo (versión 1) NO puede ser rechazado por el valor viejo del caché.
	inner.bump("u1")
	if err := svc.ValidateSession(ctx, "u1", 1); err != nil {
		t.Fatalf("a login right after a password reset must be accepted even with a stale cache: %v", err)
	}
	// Y el token viejo (versión 0) ya no sirve, porque el caché se refrescó al confirmar contra la base.
	if err := svc.ValidateSession(ctx, "u1", 0); !errors.Is(err, domain.ErrInvalidSession) {
		t.Fatalf("the old session must be revoked: %v", err)
	}
}

func TestRevokedSessionIsRejectedImmediatelyAfterClear(t *testing.T) {
	ctx := context.Background()
	inner := newFakeSessions(map[string]int64{"u1": 0})
	cache := NewSessionVersionCache(inner, time.Hour, 100)
	svc := NewAuthService(nil, cache)
	if err := svc.ValidateSession(ctx, "u1", 0); err != nil {
		t.Fatal(err)
	}
	inner.bump("u1") // otra persona (o esta instancia) cambió la contraseña
	// Sin Clear, el caché todavía acepta el token viejo: es el compromiso del TTL.
	if err := svc.ValidateSession(ctx, "u1", 0); err != nil {
		t.Fatalf("documented trade-off: stale until ttl or Clear: %v", err)
	}
	cache.Clear() // lo que dispara el cambio de contraseña en esta instancia
	if err := svc.ValidateSession(ctx, "u1", 0); !errors.Is(err, domain.ErrInvalidSession) {
		t.Fatalf("after Clear the revoked token is rejected at once: %v", err)
	}
}

func TestSessionCacheIsSafeUnderConcurrency(t *testing.T) {
	inner := newFakeSessions(map[string]int64{"u1": 0, "u2": 0, "u3": 0})
	cache := NewSessionVersionCache(inner, time.Millisecond, 4)
	svc := NewAuthService(nil, cache)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := []string{"u1", "u2", "u3"}[g%3]
			for i := 0; i < 300; i++ {
				_ = svc.ValidateSession(context.Background(), id, 0)
				if i%50 == 0 {
					cache.Clear()
				}
				if i%70 == 0 {
					cache.Invalidate(id)
				}
			}
		}(g)
	}
	wg.Wait()
}
