package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
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
		log.Fatalf("Error conectando a DB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// 1. Crear Artistas
	type Artist struct {
		ID       string
		Name     string
		Type     string
		Genres   string
		Avatar   string
	}
	
	djs := []Artist{
		{ID: uuid.NewString(), Name: "Hernán Cattaneo", Type: "DJ", Genres: `{"Progressive House", "Melodic"}`, Avatar: "https://images.unsplash.com/photo-1517592478330-8041ce0fb198?w=200&h=200&fit=crop"},
		{ID: uuid.NewString(), Name: "Nick Warren", Type: "DJ", Genres: `{"Progressive House"}`, Avatar: "https://images.unsplash.com/photo-1563842186988-1250228bb4f7?w=200&h=200&fit=crop"},
		{ID: uuid.NewString(), Name: "Amelie Lens", Type: "DJ", Genres: `{"Hard Techno"}`, Avatar: "https://images.unsplash.com/photo-1595971253018-051871f3fa14?w=200&h=200&fit=crop"},
		{ID: uuid.NewString(), Name: "Mariano Mellino", Type: "DJ", Genres: `{"Progressive House"}`, Avatar: "https://images.unsplash.com/photo-1549834125-82d3c48159a3?w=200&h=200&fit=crop"},
	}

	for _, dj := range djs {
		_, err = db.ExecContext(ctx, `
			INSERT INTO artists (id, name, artist_type, genres, avatar_url)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING
		`, dj.ID, dj.Name, dj.Type, dj.Genres, dj.Avatar)
		if err != nil {
			log.Fatalf("Error insertando %s: %v", dj.Name, err)
		}
	}
	log.Println("✅ 4 Artistas insertados en la base de datos.")

	// 2. Obtener todos los IDs de eventos
	rows, err := db.QueryContext(ctx, "SELECT id FROM events")
	if err != nil {
		log.Fatalf("Error obteniendo eventos: %v", err)
	}
	defer rows.Close()

	var eventIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			log.Fatalf("Error escaneando evento: %v", err)
		}
		eventIDs = append(eventIDs, id)
	}
	
	if len(eventIDs) == 0 {
		log.Println("⚠️ No se encontraron eventos en la BD para popular el lineup.")
		return
	}

	// 3. Asociar 2 DJs aleatorios a cada evento
	for i, eventID := range eventIDs {
		// Seleccionamos 2 djs distintos dependiendo del índice
		dj1 := djs[i%len(djs)]
		dj2 := djs[(i+1)%len(djs)]

		// DJ 1 (Headliner)
		_, err = db.ExecContext(ctx, `
			INSERT INTO event_artists (event_id, artist_id, is_headliner, performance_time)
			VALUES ($1, $2, true, $3)
			ON CONFLICT (event_id, artist_id) DO NOTHING
		`, eventID, dj1.ID, time.Now().Add(time.Hour * 2))

		// DJ 2 (Support)
		_, err = db.ExecContext(ctx, `
			INSERT INTO event_artists (event_id, artist_id, is_headliner, performance_time)
			VALUES ($1, $2, false, $3)
			ON CONFLICT (event_id, artist_id) DO NOTHING
		`, eventID, dj2.ID, time.Now())

		if err != nil {
			log.Printf("⚠️ Error insertando lineup para evento %s: %v", eventID, err)
		}
	}
	log.Printf("✅ Se le asignaron lineups a %d eventos correctamente.", len(eventIDs))
}
