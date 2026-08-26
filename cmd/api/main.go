package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"root-backend-service/internal/adapters/handlers"
	"root-backend-service/internal/adapters/repository/postgres"
	kycservice "root-backend-service/internal/services/kyc"
	s3service "root-backend-service/internal/services/s3"
	"root-backend-service/internal/services/auth"
	eventservice "root-backend-service/internal/services/event"
	"root-backend-service/internal/services/search"
	"root-backend-service/internal/services/user"

	"github.com/joho/godotenv"
)

func main() {
	// Cargar variables de entorno desde .env si existe
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// 1. Conexión a la Base de Datos PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	db, err := postgres.NewPostgresDB(dbURL)
	if err != nil {
		log.Fatalf("❌ Error conectando a PostgreSQL: %v", err)
	}
	defer db.Close()

	// 2. Inicializar base de datos
	kycRepo := postgres.NewKycRepository(db)
	if err := kycRepo.InitSchema(context.Background()); err != nil {
		log.Printf("Warning: Could not init DB schema: %v\n", err)
	}
	userRepo := postgres.NewUserRepository(db)
	eventRepo := postgres.NewEventRepository(db)

	// 3. Inicialización de Servicios y Handlers HTTP
	authService := auth.NewAuthService(userRepo)
	userService := user.NewUserService(userRepo)
	eventService := eventservice.NewEventService(eventRepo)
	searchService := search.NewSearchService(userRepo, eventRepo)

	authHandler := handlers.NewAuthHandler(authService)
	userHandler := handlers.NewUserHandler(userService)
	communityHandler := handlers.NewCommunityHandler()
	postHandler := handlers.NewPostHandler()
	eventHandler := handlers.NewEventHandler(eventService)
	crewHandler := handlers.NewCrewHandler()
	searchHandler := handlers.NewSearchHandler(searchService)

	// Inyectar dependencias para KYC
	s3Service, err := s3service.NewS3Service(context.Background())
	if err != nil {
		log.Printf("Warning: Could not initialize S3 Service: %v\n", err)
	}

	kycProvider := kycservice.NewGeminiKycProvider(s3Service)

	kycHandler := handlers.NewKycHandler(s3Service, kycProvider, kycRepo, userRepo)

	// 4. Configuración del Router con Chi
	router := handlers.NewRouter(handlers.RouterConfig{
		AuthHandler:      authHandler,
		UserHandler:      userHandler,
		PostHandler:      postHandler,
		EventHandler:     eventHandler,
		CommunityHandler: communityHandler,
		CrewHandler:      crewHandler,
		KycHandler:       kycHandler,
		SearchHandler:    searchHandler,
	})

	// 5. Configuración y Arranque del Servidor HTTP
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Canal para escuchar señales de apagado gradual (Graceful Shutdown)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("🚀 Servidor Root Backend (Mocked) iniciado en el puerto %s...", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Error crítico al iniciar el servidor: %v", err)
		}
	}()

	<-stop
	log.Println("🛑 Apagando el servidor gradualmente...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("❌ Error al detener el servidor: %v", err)
	}

	log.Println("✅ Servidor detenido correctamente.")
}
