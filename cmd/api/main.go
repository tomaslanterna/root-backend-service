package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
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
	notificationRepo := postgres.NewNotificationRepository(db)
	transferRepo := postgres.NewTransferRepository(db)
	eventRepo := postgres.NewEventRepository(db)
	postRepo := postgres.NewPostRepository(db)
	artistRepo := postgres.NewArtistRepository(db)
	surveyRepo := postgres.NewEventSurveyRepository(db)
	badgeRepo := postgres.NewBadgeRepository(db)
	communityRepo := postgres.NewCommunityRepository(db)
	matchRepo := postgres.NewMatchRepository(db)
	danceRepo := postgres.NewDanceRepository(db)
	tempCrewRepo := postgres.NewTempCrewRepository(db)
	eventRRPPRepo := postgres.NewEventRRPPRepository(db)
	moderationRepo := postgres.NewModerationRepository(db)

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
	if err := initSchema(notificationRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize notification schema: %v", err)
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
	// Caché de la versión de sesión: evita un SELECT por cada request autenticada (ver SessionVersionCache).
	sessionCacheTTL := envDuration("SESSION_CACHE_TTL", 15*time.Second)
	sessionCache := auth.NewSessionVersionCache(recoveryRepo, sessionCacheTTL, envInt("SESSION_CACHE_MAX_ENTRIES", 50000))
	recoveryService.SetOnPasswordReset(sessionCache.Clear)
	if sessionCacheTTL > 0 {
		log.Printf("Session version cache enabled (ttl=%s)", sessionCacheTTL)
	} else {
		log.Println("Session version cache disabled (SESSION_CACHE_TTL<=0): every authenticated request queries the database")
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
	var fcmSender ports.PushSender
	if os.Getenv("PUSH_ENABLED") == "true" {
		sender, err := pushadapter.NewFCM(pushCtx, os.Getenv("FIREBASE_PROJECT_ID"))
		if err != nil {
			log.Fatalf("Could not configure FCM push notifications: %v", err)
		}
		fcmSender = sender
		log.Println("FCM push (Android) enabled")
	} else {
		log.Println("Android push disabled; configure Firebase and PUSH_ENABLED=true to enable")
	}

	var apnsSender ports.PushSender
	if os.Getenv("APNS_ENABLED") == "true" {
		keyPath := os.Getenv("APNS_KEY_PATH")
		keyID := os.Getenv("APNS_KEY_ID")
		teamID := os.Getenv("APNS_TEAM_ID")
		bundleID := os.Getenv("APNS_BUNDLE_ID")
		production := os.Getenv("APNS_PRODUCTION") == "true"
		sender, err := pushadapter.NewAPNS(pushadapter.APNSConfig{
			KeyPath:    keyPath,
			KeyID:      keyID,
			TeamID:     teamID,
			BundleID:   bundleID,
			Production: production,
		})
		if err != nil {
			log.Fatalf("Could not configure APNs push notifications: %v", err)
		}
		apnsSender = sender
		log.Printf("APNs push (iOS) enabled (production=%v)", production)
	} else {
		log.Println("iOS APNs push disabled; configure APNS_ENABLED=true and APNS credentials to enable")
	}

	var pushSender ports.PushSender
	if fcmSender != nil || apnsSender != nil {
		pushSender = pushadapter.NewMultiPushSender(fcmSender, apnsSender)
	}
	// Denuncias: cada una se avisa por email a SUPPORT_EMAIL (mismo proveedor que la recuperación de contraseña).
	var reportNotifier ports.ReportNotifier
	if notifier, err := emailadapter.NewReportNotifier(emailSender, os.Getenv("SUPPORT_EMAIL")); err != nil {
		log.Fatalf("Could not configure report notifications: %v", err)
	} else if notifier != nil {
		reportNotifier = notifier
	} else {
		log.Println("Report emails disabled; configure SUPPORT_EMAIL and an email provider to receive them")
	}
	reportService := coreServices.NewReportService(moderationRepo, reportNotifier)
	// El borrado de archivos de cuentas eliminadas usa S3; sin S3 la purga queda pendiente y se reintenta.
	accountFiles, filesErr := s3service.NewS3Service(context.Background())
	if filesErr != nil {
		log.Printf("Warning: account purge cannot delete files until S3 is available: %v", filesErr)
	}
	var accountDeleter ports.ObjectDeleter
	if deleter, ok := accountFiles.(ports.ObjectDeleter); ok && accountFiles != nil {
		accountDeleter = deleter
	}
	accountService := coreServices.NewAccountService(moderationRepo, sessionCache.Invalidate, accountDeleter)
	pushService := coreServices.NewPushService(pushRepo, pushSender)
	notificationService := coreServices.NewNotificationService(notificationRepo, pushRepo, pushSender)
	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		if !*backgroundWorkers {
			return
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		pruneTicker := time.NewTicker(time.Hour)
		defer pruneTicker.Stop()
		// Avisos de eventos cada 5 minutos: "empieza pronto" (3 h antes) y la reseña (2 h después) dependen de la hora,
		// y así también les llegan a quienes marcan "voy" durante el día. El dedupe evita duplicados.
		reminderTicker := time.NewTicker(5 * time.Minute)
		defer reminderTicker.Stop()
		// Denuncias nuevas: se avisan al soporte cada minuto (las que fallan se reintentan en la próxima vuelta).
		reportTicker := time.NewTicker(time.Minute)
		defer reportTicker.Stop()
		// Cuentas eliminadas: la purga de las que cumplieron 30 días corre una vez por día de Montevideo.
		var purgedFor time.Time
		// Cumpleaños: una sola corrida por día de Montevideo (el saludo igual sale a las 10:00). Si falla, se reintenta
		// en el próximo tick; si el servidor se reinicia, vuelve a correr y el dedupe birthday:{año} evita duplicados.
		var birthdaysScheduledFor time.Time
		scheduleReminders := func() {
			if err := notificationService.ScheduleEventReminders(pushCtx); err != nil && pushCtx.Err() == nil {
				log.Printf("notification scheduler: %v", err)
			}
			today := coreServices.MontevideoDate(time.Now())
			if today.Equal(birthdaysScheduledFor) {
				return
			}
			if err := notificationService.ScheduleBirthdays(pushCtx, today); err != nil {
				if pushCtx.Err() == nil {
					log.Printf("birthday scheduler: %v", err)
				}
				return
			}
			birthdaysScheduledFor = today
		}
		purgeAccounts := func() {
			today := coreServices.MontevideoDate(time.Now())
			if today.Equal(purgedFor) {
				return
			}
			n, err := accountService.PurgeDue(pushCtx)
			if n > 0 {
				log.Printf("account purge: %d deleted accounts purged", n)
			}
			if err != nil {
				if pushCtx.Err() == nil {
					log.Printf("account purge: %v", err)
				}
				return
			}
			purgedFor = today
		}
		scheduleReminders()
		purgeAccounts()
		for {
			select {
			case <-pushCtx.Done():
				return
			case <-ticker.C:
				if err := pushService.ProcessPending(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("push worker: %v", err)
				}
				if err := notificationService.ProcessPending(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("notification worker: %v", err)
				}
			case <-reminderTicker.C:
				scheduleReminders()
				purgeAccounts()
			case <-reportTicker.C:
				if err := reportService.ProcessNotifications(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("report notifications: %v", err)
				}
			case <-pruneTicker.C:
				if err := pushRepo.Prune(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("push cleanup: %v", err)
				}
				if err := notificationService.Prune(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("notification cleanup: %v", err)
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
	if err := danceRepo.InitSchema(context.Background()); err != nil {
		log.Fatalf("Could not initialize the required dance/crew schema: %v", err)
	}
	if err := initSchema(tempCrewRepo.InitSchema); err != nil {
		log.Fatalf("Could not initialize the required temporary crew schema: %v", err)
	}

	// 3. Inicialización de Servicios
	authService := auth.NewAuthService(userRepo, sessionCache)
	// Sign in with Apple: APPLE_CLIENT_IDS (bundle id y, si hay login web, el Services ID), APPLE_TEAM_ID,
	// APPLE_KEY_ID y APPLE_PRIVATE_KEY (contenido del .p8, para revocar el acceso al borrar la cuenta).
	appleClient, err := auth.NewAppleClient(auth.AppleConfig{
		TeamID: os.Getenv("APPLE_TEAM_ID"), KeyID: os.Getenv("APPLE_KEY_ID"), PrivateKey: os.Getenv("APPLE_PRIVATE_KEY"),
		ClientIDs: strings.Split(os.Getenv("APPLE_CLIENT_IDS"), ","),
	})
	if err != nil {
		log.Fatalf("Could not configure Sign in with Apple: %v", err)
	}
	if appleClient != nil {
		authService.UseApple(appleClient, postgres.NewAppleIdentityRepository(db))
		accountService.BeforeDelete(authService.RevokeApple)
		if !appleClient.CanRevoke() {
			log.Println("Sign in with Apple enabled without APPLE_PRIVATE_KEY: account deletion cannot revoke Apple tokens")
		}
	} else {
		log.Println("Sign in with Apple disabled; set APPLE_CLIENT_IDS to enable it")
	}
	userService := user.NewUserService(userRepo, notificationService)

	aiTicketProvider := aiservice.NewGeminiTicketProvider()
	contentModerator := aiservice.NewGeminiContentModerator()
	user.UseBlocks(userService, moderationRepo)
	user.UseModerator(userService, contentModerator)
	blockService := coreServices.NewBlockService(moderationRepo, userRepo)
	eventService := eventservice.NewEventService(eventRepo, artistRepo, aiTicketProvider, contentModerator, notificationService)
	searchService := search.NewSearchService(userRepo, eventRepo, communityRepo)
	postService := coreServices.NewPostService(postRepo, communityRepo, notificationService)

	chatService := coreServices.NewChatService(chatRepo, messageRepo)
	transferService := coreServices.NewTransferService(transferRepo, chatRepo, messageRepo, notificationService)
	surveyService := survey.NewSurveyService(surveyRepo)
	communityService := community.NewCommunityService(communityRepo)
	matchService := coreServices.NewMatchService(matchRepo, notificationService)
	danceService := coreServices.NewDanceService(danceRepo, eventRepo)
	tempCrewService := coreServices.NewTempCrewService(tempCrewRepo)
	// Moderación automática (Gemini) en el contenido público. El chat queda cubierto con denuncias y bloqueos.
	coreServices.UsePostModerator(postService, contentModerator)
	coreServices.UseTempCrewModerator(tempCrewService, contentModerator)
	coreServices.UseDanceModerator(danceService, contentModerator)
	coreServices.UseChatBlocks(chatService, moderationRepo)

	// Inyectar dependencias para KYC
	s3Service, err := s3service.NewS3Service(context.Background())
	if err != nil {
		log.Printf("Warning: Could not initialize S3 Service: %v\n", err)
	}
	kycProvider := kycservice.NewGeminiKycProvider(s3Service)

	// 4. Inicialización de Handlers HTTP
	authHandler := handlers.NewAuthHandler(authService)
	userHandler := handlers.NewUserHandler(userService, s3Service)
	communityHandler := handlers.NewCommunityHandler(communityService)
	postHandler := handlers.NewPostHandler(postService, s3Service)
	eventHandler := handlers.NewEventHandler(eventService, s3Service, userRepo)
	eventHandler.SetTempCrewService(tempCrewService)
	eventHandler.SetEventRRPPService(coreServices.NewEventRRPPService(eventRRPPRepo))
	crewHandler := handlers.NewCrewHandler()
	searchHandler := handlers.NewSearchHandler(searchService, s3Service)
	kycHandler := handlers.NewKycHandler(s3Service, kycProvider, kycRepo, userRepo, notificationService)

	chatHandler := handlers.NewChatHandler(chatService, s3Service)
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
	danceHandler := handlers.NewDanceHandler(danceService, s3Service)
	danceHandler.SetTempCrewService(tempCrewService)
	tempCrewHandler := handlers.NewTempCrewHandler(tempCrewService, s3Service)
	if *backgroundWorkers {
		// Cierra las solicitudes pendientes de las crews temporales cuyo evento terminó. Idempotente y con lock en PostgreSQL.
		go func() {
			closeEnded := func() {
				if n, err := tempCrewService.CloseEnded(pushCtx); err != nil && pushCtx.Err() == nil {
					log.Printf("temporary crew close: %v", err)
				} else if n > 0 {
					log.Printf("temporary crew close: %d pending requests cancelled", n)
				}
			}
			closeEnded()
			ticker := time.NewTicker(15 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-pushCtx.Done():
					return
				case <-ticker.C:
					closeEnded()
				}
			}
		}()
	}

	// 5. Configuración del Router con Chi
	router := handlers.NewRouter(handlers.RouterConfig{
		PasswordRecovery:    handlers.NewPasswordRecoveryHandler(recoveryService),
		Moderation:          handlers.NewModerationHandler(accountService, blockService, reportService, s3Service),
		ValidateSession:     authService.ValidateSession,
		AuthHandler:         authHandler,
		UserHandler:         userHandler,
		PostHandler:         postHandler,
		EventHandler:        eventHandler,
		CommunityHandler:    communityHandler,
		CrewHandler:         crewHandler,
		KycHandler:          kycHandler,
		SearchHandler:       searchHandler,
		ChatHandler:         chatHandler,
		ChatRealtime:        chatRealtime,
		PushHandler:         handlers.NewPushHandler(pushService),
		NotificationHandler: handlers.NewNotificationHandler(notificationService),
		TransferHandler:     transferHandler,
		SurveyHandler:       surveyHandler,
		BadgeHandler:        badgeHandler,
		BadgeMedia:          handlers.NewBadgeMediaHandler(badgeRepo, s3Service),
		ReviewReminder:      handlers.NewReviewReminderHandler(coreServices.NewReviewReminderService(badgeRepo)),
		AIHandler:           aiHandler,
		MatchHandler:        matchHandler,
		DanceHandler:        danceHandler,
		TempCrewHandler:     tempCrewHandler,
	})

	// 6. Configuración y Arranque del Servidor HTTP
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
		// ReadTimeout incluye leer el body: con 15 s se cortaban las subidas de fotos desde datos móviles.
		ReadHeaderTimeout: envDuration("HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:       envDuration("HTTP_READ_TIMEOUT", 60*time.Second),
		WriteTimeout:      envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		// Mayor que el idle timeout del ALB/nginx (120 s), para que el proxy cierre primero y no reuse una conexión ya cerrada.
		IdleTimeout: envDuration("HTTP_IDLE_TIMEOUT", 130*time.Second),
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

// envDuration lee una duración (por ejemplo "15s", "2m"). "0" es válido; un valor inválido usa el default y se avisa.
func envDuration(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
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

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("%s=%q is invalid; using %d", name, raw, fallback)
		return fallback
	}
	return n
}
