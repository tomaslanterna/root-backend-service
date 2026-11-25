package postgres

import (
	"testing"
	"time"
)

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestPoolConfigDefaultsAreBounded(t *testing.T) {
	cfg := PoolConfigFromEnv(envOf(nil))
	if cfg.MaxOpen != 25 || cfg.MaxIdle != 10 || cfg.ConnMaxLifetime != 30*time.Minute || cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("an unset environment must not leave the pool unbounded: %+v", cfg)
	}
}

func TestPoolConfigReadsTheEnvironment(t *testing.T) {
	cfg := PoolConfigFromEnv(envOf(map[string]string{
		"DB_MAX_OPEN_CONNS": "60", "DB_MAX_IDLE_CONNS": "15", "DB_CONN_MAX_LIFETIME": "10m", "DB_CONN_MAX_IDLE_TIME": "90s",
	}))
	if cfg.MaxOpen != 60 || cfg.MaxIdle != 15 || cfg.ConnMaxLifetime != 10*time.Minute || cfg.ConnMaxIdleTime != 90*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestPoolConfigIdleDefaultsFollowMaxOpen(t *testing.T) {
	for open, want := range map[string]int{"2": 2, "4": 2, "8": 4, "100": 10} {
		if got := PoolConfigFromEnv(envOf(map[string]string{"DB_MAX_OPEN_CONNS": open})).MaxIdle; got != want {
			t.Errorf("max open %s: idle %d, want %d", open, got, want)
		}
	}
}

func TestPoolConfigIdleNeverExceedsOpen(t *testing.T) {
	cfg := PoolConfigFromEnv(envOf(map[string]string{"DB_MAX_OPEN_CONNS": "5", "DB_MAX_IDLE_CONNS": "50"}))
	if cfg.MaxIdle != 5 {
		t.Fatalf("idle connections above the open limit would be meaningless: %+v", cfg)
	}
}

func TestPoolConfigIgnoresInvalidValues(t *testing.T) {
	cfg := PoolConfigFromEnv(envOf(map[string]string{
		"DB_MAX_OPEN_CONNS": "muchas", "DB_MAX_IDLE_CONNS": "-3", "DB_CONN_MAX_LIFETIME": "una hora", "DB_CONN_MAX_IDLE_TIME": "-1m",
	}))
	if cfg.MaxOpen != 25 || cfg.MaxIdle != 10 || cfg.ConnMaxLifetime != 30*time.Minute || cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("a typo must fall back to the safe defaults, not to an unbounded pool: %+v", cfg)
	}
}

func TestPoolConfigAllowsAnExplicitUnlimitedPool(t *testing.T) {
	cfg := PoolConfigFromEnv(envOf(map[string]string{"DB_MAX_OPEN_CONNS": "0"}))
	if cfg.MaxOpen != 0 {
		t.Fatalf("0 is the explicit opt-out: %+v", cfg)
	}
}
