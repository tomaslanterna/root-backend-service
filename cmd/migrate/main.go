package main

import (
	"context"
	"log"
	"os"

	"root-backend-service/internal/adapters/repository/postgres"
	"github.com/joho/godotenv"
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

	artistRepo := postgres.NewArtistRepository(db)
	surveyRepo := postgres.NewEventSurveyRepository(db)

	if err := artistRepo.InitSchema(context.Background()); err != nil {
		log.Fatalf("Error inicializando artists: %v", err)
	}
	log.Println("✅ Tabla artists y event_artists creadas/verificadas con éxito.")

	if err := surveyRepo.InitSchema(context.Background()); err != nil {
		log.Fatalf("Error inicializando surveys: %v", err)
	}
	log.Println("✅ Tabla event_surveys y survey_artist_ratings creadas/verificadas con éxito.")
}
