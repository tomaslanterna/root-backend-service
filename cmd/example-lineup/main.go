// Explicit administrative setup of private DJ examples for authorized manual badges.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"log"
	"root-backend-service/internal/adapters/repository/postgres"
	"strings"
	"time"
)

func main() {
	email := flag.String("email", "", "Verified account email")
	expected := flag.String("expected-user-id", "", "Expected unique account UUID")
	eventList := flag.String("events", "", "Explicit event UUIDs, comma-separated")
	apply := flag.Bool("apply", false, "Apply private examples; default read-only")
	flag.Parse()
	if *email == "" || *expected == "" || *eventList == "" {
		log.Fatal("email, expected-user-id and events required")
	}
	config, err := godotenv.Read(".env")
	if err != nil {
		log.Fatal("Cannot read local configuration")
	}
	db, err := postgres.NewPostgresDB(config["DATABASE_URL"])
	if err != nil {
		log.Fatal("Cannot connect to database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var id string
	if err := db.QueryRowContext(ctx, `SELECT MIN(id::text) FROM users WHERE lower(email)=lower($1) HAVING COUNT(*)=1`, *email).Scan(&id); err != nil || id != *expected {
		log.Fatal("Account does not match the verified unique user")
	}
	ids := strings.Split(*eventList, ",")
	if len(ids) > 50 {
		log.Fatal("Maximum 50 explicit events")
	}
	// Validate all targets before migrating or writing any example records.
	for i, event := range ids {
		event = strings.TrimSpace(event)
		ids[i] = event
		if _, err := uuid.Parse(event); err != nil {
			log.Fatal("Invalid event UUID")
		}
		var eligible bool
		if err := db.QueryRowContext(ctx, `SELECT a.source='manual' AND cardinality(COALESCE(e.lineup,'{}'::text[]))=0
        AND NOT EXISTS(SELECT 1 FROM event_artists ea WHERE ea.event_id=e.id)
        FROM event_attendance a JOIN events e ON e.id=a.event_id WHERE a.user_id=$1 AND a.event_id=$2`, id, event).Scan(&eligible); err != nil || !eligible {
			log.Fatal("Target is not an empty-lineup manual badge owned by the verified account")
		}
	}
	if !*apply {
		fmt.Printf("Read-only: %d private example lineups eligible; no changes applied\n", len(ids))
		return
	}
	repo := postgres.NewBadgeRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		log.Fatal(err)
	}
	var created int64
	for _, event := range ids {
		n, err := repo.PrepareExampleLineup(ctx, id, event)
		if err != nil {
			log.Fatal(err)
		}
		created += n
	}
	fmt.Printf("Prepared %d private example artists; official lineups and existing reviews preserved\n", created)
}
