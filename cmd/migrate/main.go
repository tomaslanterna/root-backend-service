package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/joho/godotenv"
	"root-backend-service/internal/adapters/repository/postgres"
)

func main() {
	badgesOnly := flag.Bool("badges-only", false, "Migrate only badge/review changes")
	compatible := flag.Bool("compatible", false, "Keep older live API review writes compatible")
	flag.Parse()
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
	badgeRepo := postgres.NewBadgeRepository(db)

	if !*badgesOnly {
		if err := artistRepo.InitSchema(context.Background()); err != nil {
			log.Fatalf("Error inicializando artists: %v", err)
		}
		log.Println("✅ Tabla artists y event_artists creadas/verificadas con éxito.")
	}

	if err := badgeRepo.InitSchema(context.Background()); err != nil {
		log.Fatalf("Error inicializando asistencia: %v", err)
	}
	initSurveys := surveyRepo.InitSchema
	if *compatible {
		initSurveys = surveyRepo.InitSchemaCompatible
	}
	if err := initSurveys(context.Background()); err != nil {
		log.Fatalf("Error inicializando surveys: %v", err)
	}
	log.Println("✅ Tabla event_surveys y survey_artist_ratings creadas/verificadas con éxito.")
}
