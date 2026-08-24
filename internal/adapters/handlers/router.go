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
}

func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Configuración de CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://127.0.0.1:3000", "*"},
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
		// Búsqueda
		r.With(OptionalAuthMiddleware).Post("/search", cfg.SearchHandler.Search)

		// 1. Usuarios y Vibe Profile
		r.Post("/auth/login", cfg.AuthHandler.Login)
		r.Post("/auth/register", cfg.AuthHandler.Register)
		r.Post("/auth/google", cfg.AuthHandler.GoogleLogin)
		
		r.Get("/users/check-username", cfg.UserHandler.CheckUsername)
		r.With(OptionalAuthMiddleware).Get("/users/{username}", cfg.UserHandler.GetUser)

		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)
			r.Post("/users/{username}/follow", cfg.UserHandler.FollowUser)
			r.Delete("/users/{username}/follow", cfg.UserHandler.UnfollowUser)
			r.Get("/users/me", cfg.UserHandler.GetMe)
			r.Put("/users/me", cfg.UserHandler.UpdateMe)
		})

		// 2. Feed y Publicaciones
		r.Get("/posts", cfg.PostHandler.GetPosts)
		r.Post("/posts", cfg.PostHandler.CreatePost)
		r.Post("/posts/{id}/like", cfg.PostHandler.LikePost)
		r.Post("/posts/{id}/comments", cfg.PostHandler.CommentPost)

		// 3. Eventos y Entradas
		r.Get("/events", cfg.EventHandler.GetEvents)
		r.Get("/events/{id}", cfg.EventHandler.GetEventByID)
		r.Post("/events/{id}/rsvp", cfg.EventHandler.RSVPEvent)
		r.Get("/events/{id}/tickets", cfg.EventHandler.GetEventTickets)

		// 4. Comunidades
		r.Get("/communities", cfg.CommunityHandler.GetCommunities)
		r.Get("/communities/{id}", cfg.CommunityHandler.GetCommunityByID)
		r.Post("/communities/{id}/join", cfg.CommunityHandler.JoinCommunity)

		// 5. Crews Matcher (Event Squads)
		r.Get("/crews/deck", cfg.CrewHandler.GetDeck)
		r.Post("/crews/swipe", cfg.CrewHandler.Swipe)
		r.Get("/crews/matches", cfg.CrewHandler.GetMatches)

		// 6. KYC (Verificación de Identidad)
		r.Post("/kyc/sessions", cfg.KycHandler.CreateSession)
		r.Post("/kyc/sessions/{id}/document", cfg.KycHandler.UploadDocument)
		r.Post("/kyc/sessions/{id}/face", cfg.KycHandler.UploadFace)
		r.Post("/kyc/sessions/{id}/submit", cfg.KycHandler.SubmitSession)
		r.Get("/kyc/sessions/{id}/status", cfg.KycHandler.GetStatus)
	})

	// Webhooks (Sin Auth en este caso, se autentican con HMAC)
	r.Post("/v1/webhooks/kyc-provider", cfg.KycHandler.WebhookProvider)

	return r
}
