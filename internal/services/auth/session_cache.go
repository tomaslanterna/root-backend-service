package auth

import (
	"context"
	"sync"
	"time"

	"root-backend-service/internal/core/ports"
)

// freshSessionReader lo implementa SessionVersionCache: lee la versión de la base saltando el caché.
type freshSessionReader interface {
	GetFreshSessionVersion(ctx context.Context, userID string) (int64, error)
}

type sessionCacheEntry struct {
	version int64
	expires time.Time
}

// SessionVersionCache evita una consulta a la base por cada request autenticada: guarda la versión de sesión de
// cada usuario unos segundos. Compromiso explícito: una sesión revocada (cambio de contraseña) puede seguir
// siendo válida hasta `ttl` en OTRAS instancias; en la que procesó el cambio se invalida en el acto (Clear).
//
// Solo se guardan lecturas exitosas. Si el token trae una versión distinta a la cacheada, ValidateSession
// consulta la base (GetFreshSessionVersion) antes de rechazarlo: así un login hecho justo después de un cambio de
// contraseña no queda rechazado por un valor viejo del caché.
type SessionVersionCache struct {
	inner      ports.SessionRepository
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.RWMutex
	entries map[string]sessionCacheEntry
}

var _ ports.SessionRepository = (*SessionVersionCache)(nil)

// NewSessionVersionCache envuelve un repositorio de sesiones. ttl <= 0 deja el caché desactivado (siempre consulta).
func NewSessionVersionCache(inner ports.SessionRepository, ttl time.Duration, maxEntries int) *SessionVersionCache {
	if maxEntries <= 0 {
		maxEntries = 50000
	}
	return &SessionVersionCache{inner: inner, ttl: ttl, maxEntries: maxEntries, now: time.Now, entries: make(map[string]sessionCacheEntry)}
}

// GetPasswordAndSessionVersion es la lectura del login: va siempre a la base, y de paso refresca el caché.
func (c *SessionVersionCache) GetPasswordAndSessionVersion(ctx context.Context, userID string) (string, int64, error) {
	hash, version, err := c.inner.GetPasswordAndSessionVersion(ctx, userID)
	if err == nil {
		c.store(userID, version)
	}
	return hash, version, err
}

func (c *SessionVersionCache) GetSessionVersion(ctx context.Context, userID string) (int64, error) {
	if c.ttl > 0 {
		c.mu.RLock()
		entry, ok := c.entries[userID]
		c.mu.RUnlock()
		if ok && c.now().Before(entry.expires) {
			return entry.version, nil
		}
	}
	return c.GetFreshSessionVersion(ctx, userID)
}

func (c *SessionVersionCache) GetFreshSessionVersion(ctx context.Context, userID string) (int64, error) {
	version, err := c.inner.GetSessionVersion(ctx, userID)
	if err != nil {
		c.Invalidate(userID)
		return 0, err
	}
	c.store(userID, version)
	return version, nil
}

func (c *SessionVersionCache) store(userID string, version int64) {
	if c.ttl <= 0 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[userID]; !exists && len(c.entries) >= c.maxEntries {
		for id, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, id)
			}
		}
		if len(c.entries) >= c.maxEntries {
			// Memoria acotada: si todo sigue vigente, se vacía (el costo es volver a consultar la base).
			c.entries = make(map[string]sessionCacheEntry)
		}
	}
	c.entries[userID] = sessionCacheEntry{version: version, expires: now.Add(c.ttl)}
}

func (c *SessionVersionCache) Invalidate(userID string) {
	c.mu.Lock()
	delete(c.entries, userID)
	c.mu.Unlock()
}

// Clear descarta todo el caché. Se usa tras un cambio de contraseña (que sube la versión de sesión).
func (c *SessionVersionCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]sessionCacheEntry)
	c.mu.Unlock()
}
