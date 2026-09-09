package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"root-backend-service/internal/adapters/repository/postgres"
)

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/rootdb?sslmode=disable"
	}
	
	db, err := postgres.NewPostgresDB(dbURL)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, "SELECT id, title, date FROM events ORDER BY date DESC")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	total := 0
	future := 0
	now := time.Now()

	for rows.Next() {
		var id, title string
		var date time.Time
		rows.Scan(&id, &title, &date)
		isFuture := date.After(now)
		
		// Check if has artists
		var count int
		db.QueryRow("SELECT COUNT(*) FROM event_artists WHERE event_id = $1", id).Scan(&count)
		
		log.Printf("Evento: %s | %s | Future: %v | Artists: %d", title, date.Format("2006-01-02"), isFuture, count)
		total++
		if isFuture {
			future++
		}
	}
	log.Printf("Total events: %d | Future events: %d", total, future)
}
