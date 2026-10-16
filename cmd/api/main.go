package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	emailadapter "root-backend-service/internal/adapters/email"
	"root-backend-service/internal/adapters/handlers"
	pushadapter "root-backend-service/internal/adapters/push"
	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/ports"
	aiservice "root-backend-service/internal/services/ai"
	"root-backend-service/internal/services/auth"
	"root-backend-service/internal/services/community"
	eventservice "root-backend-service/internal/services/event"
	kycservice "root-backend-service/internal/services/kyc"
	s3service "root-backend-service/internal/services/s3"
	"root-backend-service/internal/services/search"
	survey "root-backend-service/internal/services/survey"

	coreServices "root-backend-service/internal/core/services"
	"root-backend-service/internal/services/user"

	"github.com/joho/godotenv"
)

func main() {
	initializeSchema := flag.Bool("initialize-schema", true, "Initialize schema on startup (disable after a separately verified migration)")
	backgroundWorkers := flag.Bool("background-workers", true, "Process push/email queues (disable for a local API sharing the live database)")
	listenAddress := flag.String("listen-address", "", "Override listening address, e.g. 127.0.0.1:8080")
	flag.Parse()
	// Cargar variables de entorno desde .env y FORZAR que pisen a las del sistema
	if err := godotenv.Overload(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// 1. Conexión a la Base de Datos PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	db, err := postgres.NewPostgresDB(dbURL)
	if err != nil {
		log.Fatalf("❌ Error conectando a PostgreSQL: %v", err)
	}
	defer db.Close()
	pushCtx, stopPush := context.WithCancel(context.Background())
	defer stopPush()

	// 2. Inicializar repositorios de la base de datos
	kycRepo := postgres.NewKycRepository(db)
	userRepo := postgres.NewUserRepository(db)
	chatRepo := postgres.NewChatRepository(db)
	messageRepo := postgres.NewMessageRepository(db)
	pushRepo := postgres.NewPushRepository(db)
	transferRepo := postgres.NewTransferRepository(db)
	eventRepo := postgres.NewEventRepository(db)
	postRepo := postgres.NewPostRepository(db)
	artistRepo := postgres.NewArtistRepository(db)
	surveyRepo := postgres.NewEventSurveyRepository(db)
	badgeRepo := postgres.NewBadgeRepository(db)
	communityRepo := postgres.NewCommunityRepository(db)
	matchRepo := postgres.NewMatchRepository(db)
	danceRepo := postgres.NewDanceRepository(db)

	initSchema := func(initialize func(context.Context) error) error {
		if !*initializeSchema {
			return nil
		}
		return initialize(context.Background())
	}
	if err := initSchema(messageRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required message schema: %v", err)
	}
	if err := initSchema(pushRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize push schema: %v", err)
	}
	recoveryRepo := postgres.NewPasswordRecoveryRepository(db)
	if err := initSchema(recoveryRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize password recovery schema: %v", err)
	}
	var recoverySender ports.PasswordResetSender
	emailSender, err := emailadapter.NewTransactional(os.Getenv("PASSWORD_RESET_EMAIL_PROVIDER"), os.Getenv("PASSWORD_RESET_EMAIL_API_KEY"), os.Getenv("PASSWORD_RESET_EMAIL_FROM"))
	if err != nil {
		log.Fatalf("Could not configure recovery email: %v", err)
	}
	if emailSender != nil {
		recoverySender = emailSender
	}
	recoveryService, err := auth.NewPasswordRecovery(recoveryRepo, recoverySender, os.Getenv("PASSWORD_RESET_TOKEN_KEY"), os.Getenv("FRONTEND_URL"))
	if err != nil {
		log.Fatalf("Could not configure password recovery: %v", err)
	}
	if !recoveryService.Enabled() {
		log.Println("Password recovery disabled; select and configure an email provider")
	}
	recoveryDone := make(chan struct{})
	go func() {
		defer close(recoveryDone)
		if !*backgroundWorkers || !recoveryService.Enabled() {
			return
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pushCtx.Done():
				return
			case <-ticker.C:
				for i := 0; i < 10; i++ {
					processed, err := recoveryService.ProcessNext(pushCtx)
					if err != nil {
						if pushCtx.Err() == nil {
							log.Println("Recovery email worker failed; check provider configuration and queue status")
						}
						break
					}
					if !processed {
						break
					}
				}
			}
		}
	}()
	var pushSender ports.PushSender
	if os.Getenv("PUSH_ENABLED") == "true" {
		sender, err := pushadapter.NewFCM(pushCtx, os.Getenv("FIREBASE_PROJECT_ID"))
		if err != nil {
			log.Fatalf("Could not configure push notifications: %v", err)
		}
		pushSender = sender
	} else {
		log.Println("Android push disabled; configure Firebase and PUSH_ENABLED=true to enable")
	}
	pushService := coreServices.NewPushService(pushRepo, pushSender)
	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		if !*backgroundWorkers || !pushService.Enabled() {
			return
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		pruneTicker := time.NewTicker(time.Hour)
		defer pruneTicker.Stop()
		for {
			select {
			case <-pushCtx.Done():
				return
			case <-ticker.C:
				if err := pushService.ProcessPending(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("push worker: %v", err)
				}
			case <-pruneTicker.C:
				if err := pushRepo.Prune(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("push cleanup: %v", err)
				}
			}
		}
	}()
	if err := initSchema(matchRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize matcher chat indexes: %v", err)
	}
	if err := initSchema(eventRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required event schema: %v", err)
	}
	if err := initSchema(artistRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required artist schema: %v", err)
	}
	if err := initSchema(badgeRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize attendance schema: %v", err)
	}
	if err := initSchema(surveyRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required survey schema: %v", err)
	}
	if err := initSchema(communityRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required community schema: %v", err)
	}

	// 3. Inicialización de Servicios
	authService := auth.NewAuthService(userRepo, recoveryRepo)
	userService := user.NewUserService(userRepo)

	aiTicketProvider := aiservice.NewGeminiTicketProvider()
	contentModerator := aiservice.NewGeminiContentModerator()
	eventService := eventservice.NewEventService(eventRepo, artistRepo, aiTicketProvider, contentModerator)
	searchService := search.NewSearchService(userRepo, eventRepo)
	postService := coreServices.NewPostService(postRepo, communityRepo)

	chatService := coreServices.NewChatService(chatRepo, messageRepo)
	transferService := coreServices.NewTransferService(transferRepo, chatRepo, messageRepo)
	surveyService := survey.NewSurveyService(surveyRepo)
	communityService := community.NewCommunityService(communityRepo)
	matchService := coreServices.NewMatchService(matchRepo)
	danceService := coreServices.NewDanceService(danceRepo, eventRepo)

	// Inyectar dependencias para KYC
	s3Service, err := s3service.NewS3Service(context.Background())
	if err != nil {
		log.Printf("Warning: Could not initialize S3 Service: %v\n", err)
	}
	kycProvider := kycservice.NewGeminiKycProvider(s3Service)

	// 4. Inicialización de Handlers HTTP
	authHandler := handlers.NewAuthHandler(authService)
	userHandler := handlers.NewUserHandler(userService)
	communityHandler := handlers.NewCommunityHandler(communityService)
	postHandler := handlers.NewPostHandler(postService, s3Service)
	eventHandler := handlers.NewEventHandler(eventService, s3Service)
	crewHandler := handlers.NewCrewHandler()
	searchHandler := handlers.NewSearchHandler(searchService)
	kycHandler := handlers.NewKycHandler(s3Service, kycProvider, kycRepo, userRepo)

	chatHandler := handlers.NewChatHandler(chatService)
	listenerURL := os.Getenv("CHAT_DATABASE_URL")
	if listenerURL == "" {
		listenerURL = dbURL
		// Neon transaction poolers do not support LISTEN; reuse its direct endpoint.
		if parsed, err := url.Parse(listenerURL); err == nil && strings.Contains(parsed.Host, "-pooler.") {
			parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
			listenerURL = parsed.String()
		}
	}
	chatRealtime, err := handlers.NewChatRealtime(chatService, chatRepo, messageRepo, listenerURL)
	if err != nil {
		log.Fatalf("Could not start chat realtime listener: %v", err)
	}
	chatRealtime.SetCommunityRepository(communityRepo)
	defer chatRealtime.Close()
	transferHandler := handlers.NewTransferHandler(transferService)
	surveyHandler := handlers.NewSurveyHandler(surveyService, eventService)
	badgeHandler := handlers.NewBadgeHandler(coreServices.NewBadgeService(badgeRepo))
	aiHandler := handlers.NewAIHandler()
	matchHandler := handlers.NewMatchHandler(matchService)
	danceHandler := handlers.NewDanceHandler(danceService)

	// 5. Configuración del Router con Chi
	router := handlers.NewRouter(handlers.RouterConfig{
		PasswordRecovery: handlers.NewPasswordRecoveryHandler(recoveryService),
		ValidateSession:  authService.ValidateSession,
		AuthHandler:      authHandler,
		UserHandler:      userHandler,
		PostHandler:      postHandler,
		EventHandler:     eventHandler,
		CommunityHandler: communityHandler,
		CrewHandler:      crewHandler,
		KycHandler:       kycHandler,
		SearchHandler:    searchHandler,
		ChatHandler:      chatHandler,
		ChatRealtime:     chatRealtime,
		PushHandler:      handlers.NewPushHandler(pushService),
		TransferHandler:  transferHandler,
		SurveyHandler:    surveyHandler,
		BadgeHandler:     badgeHandler,
		BadgeMedia:       handlers.NewBadgeMediaHandler(badgeRepo, s3Service),
		ReviewReminder:   handlers.NewReviewReminderHandler(coreServices.NewReviewReminderService(badgeRepo)),
		AIHandler:        aiHandler,
		MatchHandler:     matchHandler,
		DanceHandler:     danceHandler,
	})

	// 6. Configuración y Arranque del Servidor HTTP
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
	if *listenAddress != "" {
		server.Addr = *listenAddress
	}
	if !*backgroundWorkers {
		log.Println("Local API: background queues left to the existing live workers")
	}

	// Canal para escuchar señales de apagado gradual (Graceful Shutdown)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("🚀 Servidor Root Backend iniciado en %s...", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Error crítico al iniciar el servidor: %v", err)
		}
	}()

	<-stop
	stopPush()
	<-pushDone
	<-recoveryDone
	chatRealtime.Close()
	log.Println("🛑 Apagando el servidor gradualmente...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("❌ Error al detener el servidor: %v", err)
	}

	log.Println("✅ Servidor detenido correctamente.")
}
