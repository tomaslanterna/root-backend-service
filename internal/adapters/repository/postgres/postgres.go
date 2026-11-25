package postgres

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

// PoolConfig limita las conexiones que este proceso abre contra PostgreSQL (Neon). Sin tope, database/sql abre
// una conexión nueva por cada consulta concurrente: un pico de tráfico supera `max_connections` del compute y
// Neon empieza a rechazar con "too many connections".
type PoolConfig struct {
	MaxOpen         int           // DB_MAX_OPEN_CONNS (0 = sin límite, no recomendado)
	MaxIdle         int           // DB_MAX_IDLE_CONNS
	ConnMaxLifetime time.Duration // DB_CONN_MAX_LIFETIME
	ConnMaxIdleTime time.Duration // DB_CONN_MAX_IDLE_TIME
}

const (
	defaultMaxOpenConns    = 25
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
)

// PoolConfigFromEnv lee la configuración del pool. Un valor inválido no impide arrancar: se usa el valor por
// defecto y se avisa en el log.
func PoolConfigFromEnv(getenv func(string) string) PoolConfig {
	cfg := PoolConfig{MaxOpen: defaultMaxOpenConns, ConnMaxLifetime: defaultConnMaxLifetime, ConnMaxIdleTime: defaultConnMaxIdleTime}
	if raw := getenv("DB_MAX_OPEN_CONNS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			cfg.MaxOpen = n
		} else {
			log.Printf("DB_MAX_OPEN_CONNS=%q is invalid; using %d", raw, defaultMaxOpenConns)
		}
	}
	// Por defecto, la mitad de las abiertas (entre 2 y 10) se mantienen listas para el próximo pedido.
	cfg.MaxIdle = cfg.MaxOpen / 2
	if cfg.MaxIdle < 2 {
		cfg.MaxIdle = 2
	}
	if cfg.MaxIdle > 10 {
		cfg.MaxIdle = 10
	}
	if raw := getenv("DB_MAX_IDLE_CONNS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			cfg.MaxIdle = n
		} else {
			log.Printf("DB_MAX_IDLE_CONNS=%q is invalid; using %d", raw, cfg.MaxIdle)
		}
	}
	cfg.ConnMaxLifetime = durationFromEnv(getenv, "DB_CONN_MAX_LIFETIME", cfg.ConnMaxLifetime)
	cfg.ConnMaxIdleTime = durationFromEnv(getenv, "DB_CONN_MAX_IDLE_TIME", cfg.ConnMaxIdleTime)
	if cfg.MaxOpen > 0 && cfg.MaxIdle > cfg.MaxOpen {
		cfg.MaxIdle = cfg.MaxOpen
	}
	return cfg
}

func durationFromEnv(getenv func(string) string, name string, fallback time.Duration) time.Duration {
	raw := getenv(name)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		log.Printf("%s=%q is invalid; using %s", name, raw, fallback)
		return fallback
	}
	return d
}

// Apply aplica la configuración al pool de database/sql.
func (c PoolConfig) Apply(db *sql.DB) {
	db.SetMaxOpenConns(c.MaxOpen)
	db.SetMaxIdleConns(c.MaxIdle)
	db.SetConnMaxLifetime(c.ConnMaxLifetime)
	db.SetConnMaxIdleTime(c.ConnMaxIdleTime)
}

// NewPostgresDB establishes a connection to the PostgreSQL database.
func NewPostgresDB(connStr string) (*sql.DB, error) {
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL is empty")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	pool := PoolConfigFromEnv(os.Getenv)
	pool.Apply(db)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if pool.MaxOpen == 0 {
		log.Println("⚠️  DB_MAX_OPEN_CONNS=0: pool sin límite de conexiones")
	}
	log.Printf("✅ Conexión exitosa a PostgreSQL (Neon DB) — pool: max_open=%d max_idle=%d max_lifetime=%s max_idle_time=%s",
		pool.MaxOpen, pool.MaxIdle, pool.ConnMaxLifetime, pool.ConnMaxIdleTime)
	return db, nil
}
