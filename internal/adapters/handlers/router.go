package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type RouterConfig struct {
	AuthHandler      *AuthHandler
	UserHandler      *UserHandler
	PostHandler      *PostHandler
	EventHandler     *EventHandler
	CommunityHandler *CommunityHandler
	CrewHandler      *CrewHandler
	KycHandler       *KycHandler
	SearchHandler    *SearchHandler
	ChatHandler      *ChatHandler
	TransferHandler  *TransferHandler
	SurveyHandler    *SurveyHandler
	AIHandler        *AIHandler
	MatchHandler     *MatchHandler
	DanceHandler     *DanceHandler
	ChatRealtime     *ChatRealtime
	PushHandler      *PushHandler
}

func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Configuración de CORS
	r.Use(cors.Handler(cors.Options{
		// Usamos AllowOriginFunc para aceptar dinámicamente cualquier origen (ideal para Capacitor y Vercel/Netlify en conjunto)
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return true
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health Check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "OK"})
	})

	r.Route("/v1", func(r chi.Router) {
		if cfg.PushHandler != nil {
			r.With(AuthMiddleware).Get("/push/status", cfg.PushHandler.Status)
			r.With(AuthMiddleware).Put("/push/devices/{id}", cfg.PushHandler.Register)
			r.With(AuthMiddleware).Delete("/push/devices/{id}", cfg.PushHandler.Remove)
		}
		if cfg.ChatRealtime != nil {
			r.Get("/chats/ws", cfg.ChatRealtime.ServeHTTP)
		}
		// Búsqueda
		r.With(OptionalAuthMiddleware).Post("/search", cfg.SearchHandler.Search)

		// 1. Usuarios y Vibe Profile
		r.Post("/auth/login", cfg.AuthHandler.Login)
		r.Post("/auth/register", cfg.AuthHandler.Register)
		r.Post("/auth/google", cfg.AuthHandler.GoogleLogin)

		r.Get("/users/check-username", cfg.UserHandler.CheckUsername)
		r.With(OptionalAuthMiddleware).Get("/users/{username}", cfg.UserHandler.GetUser)
		r.With(OptionalAuthMiddleware).Get("/users/{username}/communities", cfg.CommunityHandler.GetUserCommunities)
		r.With(OptionalAuthMiddleware).Get("/users/{username}/events", cfg.EventHandler.GetUserEvents)

		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)
			r.Post("/users/{username}/follow", cfg.UserHandler.FollowUser)
			r.Delete("/users/{username}/follow", cfg.UserHandler.UnfollowUser)
			r.Get("/users/me", cfg.UserHandler.GetMe)
			r.Put("/users/me", cfg.UserHandler.UpdateMe)
			r.Get("/users/me/communities", cfg.CommunityHandler.GetMyCommunities)
		})

		// 2. Feed y Publicaciones
		r.With(OptionalAuthMiddleware).Get("/posts", cfg.PostHandler.GetPosts)
		r.With(OptionalAuthMiddleware).Get("/posts/{id}", cfg.PostHandler.GetPostByID)
		r.With(AuthMiddleware).Post("/posts", cfg.PostHandler.CreatePost)
		r.With(AuthMiddleware).Post("/posts/image", cfg.PostHandler.UploadImage)
		r.Post("/posts/{id}/like", cfg.PostHandler.LikePost)
		r.Route("/posts/{id}/comments", func(r chi.Router) {
			r.Get("/", cfg.PostHandler.GetPostComments)
			r.With(AuthMiddleware).Post("/", cfg.PostHandler.CommentPost)
		})

		// 3. Eventos y Entradas
		// Subida de imagen para eventos (banner)
		r.With(AuthMiddleware).Post("/events/image", cfg.EventHandler.UploadEventImage)
		// Importación masiva de eventos
		r.Post("/events/bulk-csv", cfg.EventHandler.BulkCreateEventsCSV)
		r.With(OptionalAuthMiddleware).Get("/events", cfg.EventHandler.GetEvents)
		r.Get("/events/featured", cfg.EventHandler.GetFeaturedEvents)
		r.With(AuthMiddleware).Get("/events/live-status", cfg.EventHandler.GetLiveEventStatus)
		r.With(OptionalAuthMiddleware).Get("/events/{id}", cfg.EventHandler.GetEventByID)
		r.With(AuthMiddleware).Post("/events/{id}/rsvp", cfg.EventHandler.RSVPEvent)
		r.With(AuthMiddleware).Get("/events/{id}/attendees/followed", cfg.EventHandler.GetFollowedGoingAttendees)
		r.Route("/events/{id}/comments", func(r chi.Router) {
			r.Get("/", cfg.EventHandler.GetEventComments)
			r.With(AuthMiddleware).Post("/", cfg.EventHandler.CreateEventComment)
		})
		r.Get("/events/{id}/tickets", cfg.EventHandler.GetEventTickets)

		// 4. Comunidades
		r.With(OptionalAuthMiddleware).Get("/communities", cfg.CommunityHandler.GetCommunities)
		r.With(OptionalAuthMiddleware).Get("/communities/{id}", cfg.CommunityHandler.GetCommunity)
		r.With(AuthMiddleware).Post("/communities/{id}/join", cfg.CommunityHandler.JoinCommunity)
		r.With(AuthMiddleware).Delete("/communities/{id}/membership", cfg.CommunityHandler.LeaveCommunity)
		r.With(OptionalAuthMiddleware).Get("/communities/{id}/announcements", cfg.PostHandler.GetCommunityAnnouncements)
		r.With(AuthMiddleware).Post("/communities/{id}/announcements", cfg.PostHandler.CreateCommunityAnnouncement)
		r.With(AuthMiddleware).Put("/communities/{id}/membership/preferences", cfg.CommunityHandler.SetMuted)
		r.With(AuthMiddleware).Post("/communities/{id}/read", cfg.CommunityHandler.MarkRead)
		r.With(AuthMiddleware).Put("/communities/{id}/announcements/{postID}/pin", cfg.CommunityHandler.SetPinned)
		r.With(AuthMiddleware).Post("/communities/{id}/reports", cfg.CommunityHandler.CreateReport)
		r.With(AuthMiddleware).Get("/communities/{id}/reports", cfg.CommunityHandler.GetReports)
		r.With(AuthMiddleware).Patch("/communities/{id}/reports/{reportID}", cfg.CommunityHandler.ReviewReport)

		// 5. Crews Matcher (Event Squads)
		r.Get("/crews/deck", cfg.CrewHandler.GetDeck)
		r.Post("/crews/swipe", cfg.CrewHandler.Swipe) // Old fake endpoint
		r.With(AuthMiddleware).Get("/crews/matches", cfg.MatchHandler.GetMatches)
		r.With(AuthMiddleware).Post("/crews/{id}/chat", cfg.MatchHandler.EnsureSquadChat)
		r.With(AuthMiddleware).Post("/events/{eventId}/swipes", cfg.MatchHandler.HandleSwipe)

		// 6. KYC (Verificación de Identidad)
		r.Post("/kyc/sessions", cfg.KycHandler.CreateSession)
		r.Post("/kyc/sessions/{id}/document", cfg.KycHandler.UploadDocument)
		r.Post("/kyc/sessions/{id}/face", cfg.KycHandler.UploadFace)
		r.Post("/kyc/sessions/{id}/submit", cfg.KycHandler.SubmitSession)
		r.Get("/kyc/sessions/{id}/status", cfg.KycHandler.GetStatus)

		// 7. Autenticados Generales (Encuestas)
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)
			r.Get("/users/me/pending-surveys", cfg.SurveyHandler.GetPendingSurveys)
			r.Post("/events/{id}/surveys", cfg.SurveyHandler.SubmitSurvey)
		})

		// 11. AI Assistant
		r.With(AuthMiddleware).Post("/ai/enhance-text", cfg.AIHandler.EnhanceText)

		// Transfers y Chats
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)

			// Transfers
			r.Get("/transfers", cfg.TransferHandler.GetTransfers)
			r.Post("/transfers", cfg.TransferHandler.CreateTransfer)
			r.Get("/transfers/{id}", cfg.TransferHandler.GetTransfer)
			r.Post("/transfers/{id}/start-deal", cfg.TransferHandler.StartDeal)
			r.Post("/transfers/{id}/pay", cfg.TransferHandler.PayTransfer)
			r.Patch("/transfers/{id}/status", cfg.TransferHandler.UpdateStatus)

			// Chats
			r.Get("/chats", cfg.ChatHandler.GetUserChats)
			r.Post("/chats/direct", cfg.ChatHandler.CreateDirectChat)
			r.Get("/chats/{id}", cfg.ChatHandler.GetChatByID)
			r.Get("/chats/{id}/messages", cfg.ChatHandler.GetMessages)
			r.Post("/chats/{id}/messages", cfg.ChatHandler.SendMessage)
			r.Patch("/chats/{id}/messages/read", cfg.ChatHandler.MarkMessagesRead)
			r.Post("/chats/{id}/receipts", cfg.ChatHandler.AcknowledgeMessages)
		})

		// Dance Ranker
		r.Route("/dance", func(r chi.Router) {
			r.With(AuthMiddleware).Post("/sync", cfg.DanceHandler.SyncSteps)
			r.With(AuthMiddleware).Get("/sessions", cfg.DanceHandler.GetMyDanceSessions)
		})
		r.Route("/crews", func(r chi.Router) {
			r.With(AuthMiddleware).Get("/", cfg.DanceHandler.GetMyCrews)
			r.With(AuthMiddleware).Post("/", cfg.CrewHandler.CreatePermanentCrew)
			r.With(AuthMiddleware).Post("/join/{inviteCode}", cfg.CrewHandler.JoinPermanentCrew)
			r.With(OptionalAuthMiddleware).Get("/{id}", cfg.DanceHandler.GetCrewByID)
			r.With(AuthMiddleware).Get("/{id}/rankings", cfg.DanceHandler.GetCrewLeaderboards)
			r.With(AuthMiddleware).Get("/{id}/events", cfg.DanceHandler.GetCrewEvents)
		})
	})

	// Webhooks (Sin Auth en este caso, se autentican con HMAC o IPNs de MP)
	r.Post("/v1/webhooks/kyc-provider", cfg.KycHandler.WebhookProvider)
	r.Post("/v1/webhooks/mercadopago", cfg.TransferHandler.MercadoPagoWebhook)

	return r
}
