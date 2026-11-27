// Package migrations aplica el esquema de PostgreSQL con migraciones versionadas (goose).
//
// La versión 1 (baseline.go) es el esquema que antes se aplicaba en cada arranque. Las siguientes
// son archivos SQL numerados en sql/, embebidos en el binario. Ver README.md.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed sql/*.sql
var embedded embed.FS

// lockID identifica el advisory lock de Postgres que toman las migraciones, para que varias
// instancias que arrancan a la vez no migren en paralelo.
const lockID int64 = 7_402_391_118

func newProvider(db *sql.DB) (*goose.Provider, error) {
	sqlFiles, err := fs.Sub(embedded, "sql")
	if err != nil {
		return nil, fmt.Errorf("opening embedded migrations: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(lockID),
		// Reintenta cada segundo hasta 10 minutos mientras otra instancia migra.
		lock.WithLockTimeout(1, 600),
	)
	if err != nil {
		return nil, fmt.Errorf("creating migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sqlFiles,
		goose.WithGoMigrations(baselineMigration()),
		goose.WithDisableGlobalRegistry(true),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return nil, fmt.Errorf("creating migration provider: %w", err)
	}
	return provider, nil
}

// Up aplica todas las migraciones pendientes. Es seguro llamarlo en cada arranque: si no hay
// pendientes no hace nada.
func Up(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	results, err := provider.Up(ctx)
	for _, result := range results {
		if result.Error == nil {
			name := result.Source.Path
			if name == "" {
				name = "baseline (Go)"
			}
			log.Printf("Migration %d applied: %s (%s)", result.Source.Version, name, result.Duration)
		}
	}
	if err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	log.Printf("Database schema at version %d", version)
	return nil
}
