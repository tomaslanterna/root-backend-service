package postgres

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

// NewPostgresDB establishes a connection to the PostgreSQL database.
func NewPostgresDB(connStr string) (*sql.DB, error) {
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL is empty")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("✅ Conexión exitosa a PostgreSQL (Neon DB)")
	return db, nil
}
