package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real SQL integration, isolated search_path. Never migrates or writes public data.
func TestBadgesPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("BADGES_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("BADGES_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set BADGES_TEST_DATABASE_URL for isolated PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "badges_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("isolated cleanup: %v", err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	params := parsed.Query()
	params.Del("search_path")
	params.Set("options", "-c search_path="+schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var activeSchema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&activeSchema); err != nil {
		t.Fatal(err)
	}
	if activeSchema != schema {
		t.Fatalf("refusing writes: expected isolated schema %s, got %s", schema, activeSchema)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE users(id UUID PRIMARY KEY,username TEXT UNIQUE);
	 CREATE TABLE events(id UUID PRIMARY KEY,title TEXT,date TIMESTAMPTZ,location TEXT,producer_id UUID,
	 latitude FLOAT8,longitude FLOAT8,geofence_radius_meters INT,cinematic_banner_url TEXT,description TEXT,
	 lineup TEXT[],genre TEXT,price NUMERIC,is_featured BOOLEAN,created_at TIMESTAMPTZ DEFAULT NOW(),
	 ticket_tiers JSONB,ticket_source TEXT,ticket_url TEXT,ticket_info_fetched_at TIMESTAMPTZ);
	 CREATE TABLE event_rsvps(user_id UUID,event_id UUID,status TEXT,PRIMARY KEY(user_id,event_id));
	 CREATE TABLE dance_sessions(id UUID PRIMARY KEY,user_id UUID,event_id UUID,steps_count INT,is_validated BOOLEAN);`)
	if err != nil {
		t.Fatal(err)
	}
	badges := NewBadgeRepository(db)
	surveys := NewEventSurveyRepository(db)
	events := NewEventRepository(db)
	if err := NewArtistRepository(db).InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := badges.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := surveys.InitSchemaCompatible(ctx); err != nil {
		t.Fatal(err)
	}
	var attendanceFK bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='event_surveys'::regclass AND conname='event_surveys_attendance_fk')`).Scan(&attendanceFK); err != nil || attendanceFK {
		t.Fatal("compatible rollout must not restrict the older live API", err)
	}
	for i := 0; i < 2; i++ {
		if err := badges.InitSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := surveys.InitSchema(ctx); err != nil {
			t.Fatal(err)
		}
	}
	u1, u2, e1, e2, e3 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES($1,'one'),($2,'two')`, u1, u2); err != nil {
		t.Fatal(err)
	}
	for i, e := range []string{e1, e2, e3} {
		if _, err := db.ExecContext(ctx, `INSERT INTO events(id,title,date,location,latitude,longitude,geofence_radius_meters) VALUES($1,'Fiesta',NOW()-($2*INTERVAL '1 hour'),'Montevideo',-34.9,-56.2,300)`, e, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO event_rsvps VALUES($1,$2,'going')`, u1, e); err != nil {
			t.Fatal(err)
		}
	}
	if page, total, err := badges.List(ctx, "one", u1, 12, 0); err != nil || total != 0 || len(page) != 0 {
		t.Fatal("RSVP created a badge")
	}
	if pending, err := events.GetPendingSurveys(ctx, u1); err != nil || len(pending) != 0 {
		t.Fatal("RSVP unlocked a review")
	}
	if _, err := events.GetLiveEventStatus(ctx, u1, 0, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("outside geofence: %v", err)
	}
	if _, err := events.GetLiveEventStatus(ctx, u2, -34.9, -56.2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("non-going RSVP got attendance")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := events.GetLiveEventStatus(ctx, u1, -34.9, -56.2); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	page, total, err := badges.List(ctx, "one", u1, 1, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].EventID != e3 || page[0].Review != nil {
		t.Fatalf("idempotent presence: %+v total=%d err=%v", page, total, err)
	}
	firstConfirmed := page[0].ConfirmedAt
	if page[0].Headliner != nil {
		t.Fatal("event without headliner must not invent an artist")
	}
	mainArtist, supportArtist := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO artists(id,name,artist_type,avatar_url) VALUES($1,'Main DJ','DJ','https://example.test/main.jpg'),($2,'Support DJ','DJ',NULL);
`, mainArtist, supportArtist); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO event_artists(event_id,artist_id,is_headliner,performance_time) VALUES($1,$2,true,NOW()),($1,$3,false,NOW()-INTERVAL '1 hour')`, e3, mainArtist, supportArtist); err != nil {
		t.Fatal(err)
	}
	withArtist, err := badges.Get(ctx, u1, e3)
	if err != nil || withArtist.Headliner == nil || withArtist.Headliner.ID != mainArtist || withArtist.Headliner.Name != "Main DJ" || withArtist.Headliner.ImageURL != "https://example.test/main.jpg" {
		t.Fatalf("wrong headliner metadata: %+v, %v", withArtist, err)
	}
	if withArtist.Steps != nil || withArtist.MostDanced || len(withArtist.Lineup) != 2 || withArtist.Lineup[0].Artist.Name != "Support DJ" {
		t.Fatal("missing steps or chronological lineup incorrect", withArtist)
	}
	text := "una gran noche"
	zero := &domain.EventSurvey{ID: uuid.NewString(), UserID: u1, EventID: e3, GeneralRating: 0, Comment: &text, CreatedAt: time.Now()}
	if err := surveys.CreateSurvey(ctx, zero, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: mainArtist, Rating: 0}}); err != nil {
		t.Fatal(err)
	}
	firstID := zero.ID
	if _, err := db.ExecContext(ctx, `DELETE FROM event_rsvps WHERE user_id=$1 AND event_id=$2`, u1, e3); err != nil {
		t.Fatal(err)
	}
	if _, err := events.GetLiveEventStatus(ctx, u1, -34.9, -56.2); err != nil {
		t.Fatal(err)
	}
	if pending, err := events.GetPendingSurveys(ctx, u1); err != nil || len(pending) != 1 {
		t.Fatalf("pending confirmed attendance: %v %v", pending, err)
	}
	zero.ID = uuid.NewString()
	zero.GeneralRating = 5
	attemptedComment := "This must not overwrite the confirmed text"
	zero.Comment = &attemptedComment
	if err := surveys.CreateSurvey(ctx, zero, nil); !errors.Is(err, domain.ErrSurveyConfirmed) {
		t.Fatal("confirmed review was editable", err)
	}
	zero.ID = firstID
	if err := surveys.CreateSurvey(ctx, &domain.EventSurvey{ID: uuid.NewString(), UserID: u2, EventID: e3, GeneralRating: 5, CreatedAt: time.Now()}, nil); !errors.Is(err, domain.ErrAttendanceRequired) {
		t.Fatal("another user could review")
	}
	badge, err := badges.Get(ctx, u1, e3)
	if err != nil || badge.Review == nil || badge.Review.GeneralRating != 0 || *badge.Review.Comment != text || !badge.ConfirmedAt.Equal(firstConfirmed) {
		t.Fatal("persistent review / attendance changed", err)
	}
	public, total, err := badges.List(ctx, "one", u2, 1, 0)
	if err != nil || total != 2 || public[0].Review != nil {
		t.Fatal("private review leaked or incorrect total")
	}
	page, total, err = badges.List(ctx, "one", u1, 1, 1)
	if err != nil || total != 2 || len(page) != 1 || page[0].EventID != e3 {
		t.Fatal("pagination or chronological order incorrect")
	}
	if _, err := db.ExecContext(ctx, `UPDATE events SET date=NOW()-INTERVAL '20 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := events.GetLiveEventStatus(ctx, u1, -34.9, -56.2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("past event confirmed")
	}
	if _, err := badges.Get(ctx, u1, e3); err != nil {
		t.Fatal("badge not permanent")
	}
	// Annual winner is computed across ALL attendances, not just the requested page.
	if _, err := db.ExecContext(ctx, `INSERT INTO event_attendance(user_id,event_id,source,confirmed_at) VALUES($1,$2,'geofence','2025-12-31T23:30:00-03')`, u1, e1); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		user, event string
		steps       int
		valid       bool
	}{
		{u1, e3, 100, true}, {u1, e3, 200, true}, {u1, e2, 300, true}, {u1, e2, 999999, false}, {u1, e2, -999, true}, {u1, e1, 10, true}, {u2, e3, 999999, true},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO dance_sessions VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), row.user, row.event, row.steps, row.valid); err != nil {
			t.Fatal(err)
		}
	}
	page, _, err = badges.List(ctx, "one", u1, 1, 1)
	if err != nil || len(page) != 1 || page[0].Steps == nil || *page[0].Steps != 300 || page[0].MostDanced {
		t.Fatal("page-local ranking or duplicate/session/user leakage", page, err)
	}
	latest, err := badges.Get(ctx, u1, e2)
	if err != nil || !latest.MostDanced || latest.Steps == nil || *latest.Steps != 300 {
		t.Fatal("deterministic latest attendance tie-break failed", latest, err)
	}
	previousYear, err := badges.Get(ctx, u1, e1)
	if err != nil || !previousYear.MostDanced {
		t.Fatal("calendar year must use attendance in Montevideo, not UTC", previousYear, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE dance_sessions SET steps_count=0 WHERE user_id=$1 AND is_validated=true AND steps_count>=0`, u1); err != nil {
		t.Fatal(err)
	}
	all, _, err := badges.List(ctx, "one", u1, 12, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		if b.MostDanced || b.Steps == nil || *b.Steps != 0 {
			t.Fatal("zero real steps must stay zero without an award", b)
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE dance_sessions RENAME TO unavailable_dance_fixture`); err != nil {
		t.Fatal(err)
	}
	if err := badges.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	noDance, _, err := badges.List(ctx, "one", u1, 12, 0)
	if err != nil {
		t.Fatal("missing dance infrastructure must not break badges", err)
	}
	for _, b := range noDance {
		if b.Steps != nil || b.MostDanced {
			t.Fatal("missing infrastructure invented steps", b)
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE unavailable_dance_fixture RENAME TO dance_sessions`); err != nil {
		t.Fatal(err)
	}
	if err := badges.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// A manual grant never pretends to be GPS presence or fabricates activity.
	future := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO events(id,title,date,location) VALUES($1,'Future manual grant',NOW()+INTERVAL '1 year','Montevideo')`, future); err != nil {
		t.Fatal(err)
	}
	if _, err := badges.GrantManual(ctx, u2, []string{future, uuid.NewString()}, "Authorized test grant"); err == nil {
		t.Fatal("missing event must abort the complete grant")
	}
	if page, total, err := badges.List(ctx, "two", u2, 12, 0); err != nil || total != 0 || len(page) != 0 {
		t.Fatal("partial grant escaped rollback", err)
	}
	if n, err := badges.GrantManual(ctx, u2, []string{future}, "Authorized test grant"); err != nil || n != 1 {
		t.Fatal("manual grant failed", n, err)
	}
	manual, err := badges.Get(ctx, u2, future)
	if err != nil || manual.Source != "manual" || manual.Review != nil || manual.Steps != nil || manual.MostDanced {
		t.Fatal("manual grant invented data", manual, err)
	}
	if n, err := badges.GrantManual(ctx, u2, []string{future}, "Second authorized request"); err != nil || n != 0 {
		t.Fatal("manual retry duplicated the badge", n, err)
	}
	var persistedReason string
	if err := db.QueryRowContext(ctx, `SELECT grant_reason FROM event_attendance WHERE user_id=$1 AND event_id=$2`, u2, future).Scan(&persistedReason); err != nil || persistedReason != "Authorized test grant" {
		t.Fatal("retry overwrote grant audit", err)
	}
	if n, err := badges.GrantManual(ctx, u1, []string{e3}, "Should preserve geofence"); err != nil || n != 0 {
		t.Fatal("manual grant overwrote attendance", n, err)
	}
	preserved, err := badges.Get(ctx, u1, e3)
	if err != nil || preserved.Source != "geofence" || !preserved.ConfirmedAt.Equal(firstConfirmed) || preserved.Review == nil || preserved.Review.GeneralRating != 0 {
		t.Fatal("grant changed original attendance/review", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO event_attendance(user_id,event_id,source) VALUES($1,$2,'manual')`, u2, e1); err == nil {
		t.Fatal("manual source needs an audit reason")
	}
	// Confirmation makes both event and individual ratings immutable.
	zero.ArtistRatings = nil
	if err := surveys.CreateSurvey(ctx, zero, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: mainArtist, Rating: 0}}); !errors.Is(err, domain.ErrSurveyConfirmed) {
		t.Fatal("confirmed review accepted a second save", err)
	}
	var ratingID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM survey_artist_ratings WHERE survey_id=$1 AND artist_id=$2`, zero.ID, mainArtist).Scan(&ratingID); err != nil {
		t.Fatal(err)
	}
	if err := surveys.CreateSurvey(ctx, zero, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: mainArtist, Rating: 5}}); !errors.Is(err, domain.ErrSurveyConfirmed) {
		t.Fatal("confirmed DJ rating could change", err)
	}
	var editedID string
	var editedRating int
	if err := db.QueryRowContext(ctx, `SELECT id,rating FROM survey_artist_ratings WHERE survey_id=$1 AND artist_id=$2`, zero.ID, mainArtist).Scan(&editedID, &editedRating); err != nil || ratingID != editedID || editedRating != 0 {
		t.Fatal("confirmed DJ rating changed", err)
	}
	invalidReview := &domain.EventSurvey{ID: uuid.NewString(), UserID: u1, EventID: e1, GeneralRating: 4, CreatedAt: time.Now()}
	if err := surveys.CreateSurvey(ctx, invalidReview, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: uuid.NewString(), Rating: 4}}); !errors.Is(err, domain.ErrInvalidSurvey) {
		t.Fatal("foreign artist accepted", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE events SET department='Canelones',country='UY' WHERE id=$1`, e1); err != nil {
		t.Fatal(err)
	}
	opening := uuid.NewString()
	if b, err := badges.OpenReminder(ctx, u1, opening); err != nil || b != nil {
		t.Fatal("reminder before departure", err)
	}
	if err := badges.Checkout(ctx, u1, e1); err != nil {
		t.Fatal(err)
	}
	if b, err := badges.OpenReminder(ctx, u1, opening); err != nil || b != nil {
		t.Fatal("same-opening reminder", err)
	}
	nextOpening := uuid.NewString()
	if b, err := badges.OpenReminder(ctx, u1, nextOpening); err != nil || b == nil || b.EventID != e1 || b.Department == nil || *b.Department != "Canelones" || b.Country == nil || *b.Country != "UY" {
		t.Fatal("next opening or real location metadata", b, err)
	}
	if err := badges.Checkout(ctx, u1, e2); err != nil {
		t.Fatal(err)
	}
	if b, err := badges.OpenReminder(ctx, u1, nextOpening); err != nil || b != nil {
		t.Fatal("multiple invitations in same opening", err)
	}
	if b, err := badges.OpenReminder(ctx, u1, uuid.NewString()); err != nil || b == nil || b.EventID != e2 {
		t.Fatal("next pending invitation", b, err)
	}
	if err := badges.Checkout(ctx, u1, e3); err != nil {
		t.Fatal(err)
	}
	if b, err := badges.OpenReminder(ctx, u1, uuid.NewString()); err != nil || b != nil {
		t.Fatal("completed or previously offered review repeated", err)
	}
	if err := badges.Checkout(ctx, u2, future); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("manual/future grant enabled checkout", err)
	}
	// Authorized fictional DJs are private to one manual badge, never official.
	if n, err := badges.PrepareExampleLineup(ctx, u2, future); err != nil || n != 3 {
		t.Fatal("preparing private examples", n, err)
	}
	if n, err := badges.PrepareExampleLineup(ctx, u2, future); err != nil || n != 0 {
		t.Fatal("example retry duplicated artists", n, err)
	}
	examples, err := badges.Get(ctx, u2, future)
	if err != nil || len(examples.Lineup) != 3 {
		t.Fatal("private example lineup missing", err)
	}
	for i, entry := range examples.Lineup {
		if entry.Artist == nil || !entry.Artist.IsExample || entry.Artist.Name != fmt.Sprintf("DJ %d", i+1) {
			t.Fatal("example was presented as a real artist", entry)
		}
	}
	if official, err := NewArtistRepository(db).GetEventLineup(ctx, future); err != nil || len(official) != 0 {
		t.Fatal("examples polluted official lineup", err)
	}
	if other, err := badges.ExampleLineup(ctx, u1, future); err != nil || len(other) != 0 {
		t.Fatal("examples exposed to another user", err)
	}
	if _, err := badges.PrepareExampleLineup(ctx, u1, e1); !errors.Is(err, domain.ErrInvalidSurvey) {
		t.Fatal("examples replaced a real lineup", err)
	}
	exampleArtist := examples.Lineup[0].ArtistID
	if err := surveys.CreateSurvey(ctx, invalidReview, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: exampleArtist, Rating: 4}}); !errors.Is(err, domain.ErrInvalidSurvey) {
		t.Fatal("another user's private example accepted", err)
	}
	manualReview := &domain.EventSurvey{ID: uuid.NewString(), UserID: u2, EventID: future, GeneralRating: 0, CreatedAt: time.Now()}
	exampleReviewText := "Mi reseña de ejemplo"
	manualReview.Comment = &exampleReviewText
	if err := surveys.CreateSurvey(ctx, manualReview, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: exampleArtist, Rating: 0}}); err != nil {
		t.Fatal("saving event/text/private DJ review", err)
	}
	if err := surveys.CreateSurvey(ctx, manualReview, []domain.SurveyArtistRating{{ID: uuid.NewString(), ArtistID: exampleArtist, Rating: 5}}); !errors.Is(err, domain.ErrSurveyConfirmed) {
		t.Fatal("private confirmed DJ rating could change", err)
	}
	updated, err := badges.Get(ctx, u2, future)
	if err != nil || updated.Review == nil || updated.Review.GeneralRating != 0 || updated.Review.Comment == nil || *updated.Review.Comment != exampleReviewText || len(updated.Review.ArtistRatings) != 1 || updated.Review.ArtistRatings[0].Rating != 0 {
		t.Fatal("review did not persist or was duplicated", err)
	}
	if err := badges.Checkout(ctx, u2, e1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("another user's checkout", err)
	}
	if b, err := badges.OpenReminder(ctx, u2, uuid.NewString()); err != nil || b != nil {
		t.Fatal("manual grant prompted a real attendance review", err)
	}
	// Two concurrent confirmations must yield exactly one immutable review.
	raceEvent := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO events(id,title,date,location) VALUES($1,'Concurrent confirmation',NOW(),'Montevideo')`, raceEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := badges.GrantManual(ctx, u2, []string{raceEvent}, "Isolated concurrency test"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, score := range []int{1, 5} {
		go func(score int) {
			results <- surveys.CreateSurvey(ctx, &domain.EventSurvey{ID: uuid.NewString(), UserID: u2, EventID: raceEvent, GeneralRating: score, CreatedAt: time.Now()}, nil)
		}(score)
	}
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrSurveyConfirmed) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent confirmation did not lock the first review", successes, conflicts)
	}
	if os.Getenv("BADGES_TEST_PREVIEW_PORT") != "" {
		serveBadgePreview(t, db, u1)
	}
}

func TestManualGrantValidation(t *testing.T) {
	repo := NewBadgeRepository(nil) // Invalid input must never reach the database.
	user, event := uuid.NewString(), uuid.NewString()
	for _, input := range []struct {
		user   string
		events []string
		reason string
	}{
		{"bad", []string{event}, "reason"}, {user, nil, "reason"}, {user, []string{event}, "  "},
		{user, []string{event}, strings.Repeat("é", 501)}, {user, []string{"invalid"}, "reason"},
		{user, []string{event, event}, "reason"}, {user, make([]string, 51), "reason"},
	} {
		if _, err := repo.GrantManual(context.Background(), input.user, input.events, input.reason); err == nil {
			t.Fatal("invalid grant accepted", input)
		}
	}
}
