package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"net"
	"net/http"
	"net/url"
	"os"
	"root-backend-service/internal/adapters/handlers"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
	"root-backend-service/internal/services/event"
	"root-backend-service/internal/services/survey"
	"sort"
	"strings"
	"testing"
	"time"
)

// Opt-in, localhost-only visual QA using the isolated SQL fixture. Never compiled into the API.
// The fixed credentials below belong exclusively to this disposable test schema.
func serveBadgePreview(t *testing.T, db *sql.DB, userID string) {
	port := os.Getenv("BADGES_TEST_PREVIEW_PORT")
	lifetime := 4 * time.Minute
	if configured := os.Getenv("BADGES_TEST_PREVIEW_DURATION"); configured != "" {
		var err error
		lifetime, err = time.ParseDuration(configured)
		if err != nil || lifetime <= 0 || lifetime > time.Hour {
			t.Fatal("BADGES_TEST_PREVIEW_DURATION must be between 0 and 1h")
		}
	}
	t.Setenv("JWT_SECRET", "disposable-badge-preview-only")
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	if source := os.Getenv("BADGES_TEST_PREVIEW_EVENTS_URL"); source != "" {
		importBadgePreviewEvents(t, db, userID, source)
	}
	if source := os.Getenv("BADGES_TEST_PREVIEW_COPY_FROM"); source != "" {
		preservePreviewReviews(t, db, userID, source)
	}
	if os.Getenv("BADGES_TEST_PREVIEW_SAMPLES") == "1" {
		seedBadgePreviewSamples(t, db, userID)
	}
	router := chi.NewRouter()
	user := map[string]any{"id": userID, "username": "one", "name": "Perfil de prueba", "role": "USER", "isKycVerified": true, "followers": []string{}, "following": []string{}, "dob": "1995-01-01", "documentId": "preview-only", "country": "UY"}
	router.Post("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Email, Password string }
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Email != "badges@example.test" || body.Password != "PreviewOnly123!" {
			http.Error(w, "invalid fixture login", 401)
			return
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(os.Getenv("JWT_SECRET")))
		if err != nil {
			http.Error(w, "token error", 500)
			return
		}
		write(w, map[string]any{"token": token, "user": user})
	})
	router.Get("/v1/users/one", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"user": user, "isFollowing": false})
	})
	router.With(handlers.AuthMiddleware).Get("/v1/users/me", func(w http.ResponseWriter, r *http.Request) { write(w, user) })
	for _, path := range []string{"/v1/users/one/communities", "/v1/users/one/events", "/v1/events/featured", "/v1/posts"} {
		router.Get(path, func(w http.ResponseWriter, r *http.Request) { write(w, []any{}) })
	}
	for _, path := range []string{"/v1/dance/sessions", "/v1/chats"} {
		router.Get(path, func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"data": []any{}}) })
	}
	badgeHandler := handlers.NewBadgeHandler(services.NewBadgeService(NewBadgeRepository(db)))
	eventService := event.NewEventService(NewEventRepository(db), NewArtistRepository(db), nil, nil)
	eventHandler := handlers.NewEventHandler(eventService, nil)
	router.With(handlers.OptionalAuthMiddleware).Get("/v1/events", eventHandler.GetEvents)
	surveyHandler := handlers.NewSurveyHandler(survey.NewSurveyService(NewEventSurveyRepository(db)), eventService)
	router.With(handlers.OptionalAuthMiddleware).Get("/v1/users/{username}/badges", badgeHandler.List)
	router.With(handlers.AuthMiddleware).Get("/v1/events/{id}/badge", badgeHandler.Get)
	router.With(handlers.AuthMiddleware).Get("/v1/events/live-status", eventHandler.GetLiveEventStatus)
	router.With(handlers.OptionalAuthMiddleware).Get("/v1/events/{id}", eventHandler.GetEventByID)
	router.With(handlers.AuthMiddleware).Post("/v1/events/{id}/surveys", surveyHandler.SubmitSurvey)
	router.With(handlers.AuthMiddleware).Get("/v1/users/me/pending-surveys", surveyHandler.GetPendingSurveys)
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3001")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		router.ServeHTTP(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Log("Isolated badge preview ready on localhost:" + port + " (lifetime " + lifetime.String() + ")")
	defer server.Shutdown(context.Background())
	select {
	case <-time.After(lifetime):
	case <-context.Background().Done():
	}
}

// Explicitly requested visual examples, ONLY in the disposable QA schema. Never
// used by cmd/api, presence tracking or production. No artist/lineup is invented.
func seedBadgePreviewSamples(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var schema, username string
	if err := tx.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil || !strings.HasPrefix(schema, "badges_test_") {
		t.Fatal("refusing sample steps/reviews outside isolated QA schema", err)
	}
	qualified := pq.QuoteIdentifier(schema) + "."
	if err := tx.QueryRow(`SELECT username FROM `+qualified+`users WHERE id=$1`, userID).Scan(&username); err != nil || username != "one" {
		t.Fatal("samples require the fictitious QA user one", err)
	}
	rows, err := tx.Query(`SELECT event_id FROM `+qualified+`event_attendance WHERE user_id=$1 ORDER BY confirmed_at DESC,event_id DESC`, userID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 7 {
		t.Fatal("visual samples require exactly seven QA badges")
	}
	// Leave one without step data and three unreviewed. Includes saved zero,
	// several arc lengths and one reviewed annual winner for visual comparison.
	steps := []int{12450, 18720, 8500, 6400, 9200, 0}
	ratings := []int{5, 4, 3, 0}
	comments := []string{
		"Reseña de muestra: una noche increíble.\nEl cierre fue mi momento favorito.",
		"Reseña de muestra: no paramos de bailar. Muy buena energía en la pista.",
		"Reseña de muestra: disfruté la música, aunque había bastante gente.",
		"Reseña de muestra con nota 0: esta noche no fue para mí.",
	}
	for i, id := range ids {
		if i < len(steps) {
			if _, err := tx.Exec(`INSERT INTO `+qualified+`dance_sessions(id,user_id,event_id,steps_count,is_validated)
			 VALUES($1,$2,$3,$4,true)`, uuid.NewString(), userID, id, steps[i]); err != nil {
				t.Fatal(err)
			}
		}
		if i < len(ratings) {
			// Preserve any manually edited review copied from the previous preview.
			if _, err := tx.Exec(`INSERT INTO `+qualified+`event_surveys(id,user_id,event_id,general_rating,comment,created_at)
			 VALUES($1,$2,$3,$4,$5,NOW()) ON CONFLICT(user_id,event_id) DO NOTHING`, uuid.NewString(), userID, id, ratings[i], comments[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Log("QA ONLY: six sample step totals and up to four sample reviews loaded; no live data changed")
	badges, total, err := NewBadgeRepository(db).List(context.Background(), "one", userID, 12, 0)
	if err != nil || total != 7 || len(badges) != 7 {
		t.Fatal("sample badge list failed", err)
	}
	for i, badge := range badges {
		if (i < len(steps) && (badge.Steps == nil || *badge.Steps != int64(steps[i]))) || (i >= len(steps) && badge.Steps != nil) || badge.MostDanced != (i == 1) {
			t.Fatal("sample steps/ranking not reflected in real badge query", i)
		}
		if i < len(ratings) && badge.Review == nil {
			t.Fatal("sample review missing", i)
		}
	}
}

// Preserve the user's manual QA edits when refreshing the disposable localhost
// preview. This helper cannot read an arbitrary remote account or write live data.
func preservePreviewReviews(t *testing.T, db *sql.DB, userID, source string) {
	t.Helper()
	endpoint, err := url.Parse(source)
	if err != nil || endpoint.Scheme != "http" || (endpoint.Hostname() != "localhost" && endpoint.Hostname() != "127.0.0.1") {
		t.Fatal("preview copy must use localhost HTTP")
	}
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil || !strings.HasPrefix(schema, "badges_test_") {
		t.Fatal("refusing QA copy into live schema", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Post(strings.TrimRight(source, "/")+"/v1/auth/login", "application/json", strings.NewReader(`{"email":"badges@example.test","password":"PreviewOnly123!"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(response.Body).Decode(&login)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || login.Token == "" {
		t.Fatal("old fixture login failed", err)
	}
	request, err := http.NewRequest("GET", strings.TrimRight(source, "/")+"/v1/users/one/badges?limit=50", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Data []domain.EventBadge `json:"data"`
	}
	err = json.NewDecoder(response.Body).Decode(&page)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("old fixture badges failed", err)
	}
	for _, b := range page.Data {
		result, err := db.Exec(`UPDATE `+pq.QuoteIdentifier(schema)+`.event_attendance SET confirmed_at=$3 WHERE user_id=$1 AND event_id=$2`, userID, b.EventID, b.ConfirmedAt)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 || b.Review == nil {
			continue
		}
		b.Review.ID = uuid.NewString()
		b.Review.UserID = userID
		if err := NewEventSurveyRepository(db).CreateSurvey(context.Background(), b.Review, nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("Preserved existing localhost QA attendance dates and personal review text/scores; no steps fabricated")
}

// Read-only public catalog import. All assignments are synthetic QA fixtures in the
// disposable schema, not claims of attendance and never writes to public tables.
func importBadgePreviewEvents(t *testing.T, db *sql.DB, userID, source string) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	var catalog []domain.Event
	for offset := 0; ; {
		endpoint, err := url.Parse(source)
		if err != nil || endpoint.Scheme != "https" {
			t.Fatal("preview catalog must be a public HTTPS endpoint")
		}
		query := endpoint.Query()
		query.Set("limit", "50")
		query.Set("offset", fmt.Sprint(offset))
		endpoint.RawQuery = query.Encode()
		response, err := client.Get(endpoint.String())
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Data []domain.Event `json:"data"`
			Meta struct {
				HasMore bool `json:"hasMore"`
			} `json:"meta"`
		}
		err = json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("public catalog response %d, decode error %v", response.StatusCode, err)
		}
		catalog = append(catalog, page.Data...)
		if !page.Meta.HasMore {
			break
		}
		if len(page.Data) == 0 || len(catalog) > 10000 {
			t.Fatal("invalid preview catalog pagination")
		}
		offset += len(page.Data)
	}
	if len(catalog) == 0 {
		t.Fatal("empty public preview catalog")
	}
	// Keep the same seven QA memories as their dates pass. The public list hides
	// past events, but their public details remain accessible by ID.
	selected := map[string]bool{
		"6b11d7e0-6461-477f-abc6-4eb3dd7f3088": true,
		"aaeb5120-8a38-41be-9734-eddd9f5dd404": true,
		"7d8539cb-8fad-49ba-ad22-020eae33f625": true,
		"bf8bd8e3-6f0a-4732-8f49-8e4f90961c3e": true,
		"cd2f7c89-7cf3-48fa-ad3b-cd91be91894a": true,
		"b1de0f16-4e46-456c-9a14-f3b1a17bc968": true,
		"9d15f159-f9cc-49ae-8785-c2c98699d659": true,
	}
	present := make(map[string]bool, len(catalog))
	for _, event := range catalog {
		present[event.ID] = true
	}
	for id := range selected {
		if present[id] {
			continue
		}
		endpoint, err := url.Parse(source)
		if err != nil || endpoint.Scheme != "https" {
			t.Fatal("preview detail must use the public HTTPS catalog", err)
		}
		endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + id
		endpoint.RawQuery = ""
		response, err := client.Get(endpoint.String())
		if err != nil {
			t.Fatal("reading past QA event", err)
		}
		var event domain.Event
		err = json.NewDecoder(response.Body).Decode(&event)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil || event.ID != id {
			t.Fatalf("past QA event %s response %d, decode error %v", id, response.StatusCode, err)
		}
		catalog = append(catalog, event)
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Date.Equal(catalog[j].Date) {
			return catalog[i].ID < catalog[j].ID
		}
		return catalog[i].Date.Before(catalog[j].Date)
	})
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var schema string
	if err := tx.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil || !strings.HasPrefix(schema, "badges_test_") {
		t.Fatal("refusing fixture replacement outside badges_test schema", err)
	}
	// Explicit schema-qualified targets prevent touching live data even with a changed search_path.
	for _, table := range []string{"survey_artist_ratings", "event_surveys", "event_attendance", "dance_sessions", "event_rsvps", "event_artists", "events"} {
		if _, err := tx.Exec(`DELETE FROM ` + pq.QuoteIdentifier(schema) + `.` + pq.QuoteIdentifier(table)); err != nil {
			t.Fatal(err)
		}
	}
	// Only copy published data. Never infer a lineup from the title, even in QA.
	assigned := 0
	for i, e := range catalog {
		// Ticket URLs/cache are intentionally not copied: preview has no scraping service.
		if _, err := tx.Exec(`INSERT INTO events(id,title,date,location,description,cinematic_banner_url,lineup,genre,price,is_featured)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, e.ID, e.Title, e.Date, e.Location, e.Description, e.CinematicBannerURL, pq.Array(e.Lineup), e.Genre, e.Price, e.IsFeatured); err != nil {
			t.Fatal(err)
		}
		if selected[e.ID] {
			if _, err := tx.Exec(`INSERT INTO event_attendance(user_id,event_id,source,confirmed_at) VALUES($1,$2,'geofence',NOW()-($3*INTERVAL '1 minute'))`, userID, e.ID, i); err != nil {
				t.Fatal(err)
			}
			assigned++
		}
	}
	if assigned != 7 {
		t.Fatalf("expected exactly 7 preview badges, found %d", assigned)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Copied %d public events into %s; assigned 7 DEMO badges to fixture user one only", len(catalog), schema)
}
