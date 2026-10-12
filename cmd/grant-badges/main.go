// Administrative manual grants. Requires DB access; not a public API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"root-backend-service/internal/adapters/repository/postgres"
)

func main() {
	email := flag.String("email", "", "Exact account email")
	expected := flag.String("expected-user-id", "", "Previously verified account UUID")
	events := flag.String("events", "", "Comma-separated event UUIDs")
	reason := flag.String("reason", "", "Audit reason, max 500 characters")
	apply := flag.Bool("apply", false, "Explicitly authorize schema preparation and grants; otherwise read-only")
	flag.Parse()
	if *email == "" || *events == "" || strings.TrimSpace(*reason) == "" || (*apply && *expected == "") {
		log.Fatal("email, events and reason are required; apply also requires expected-user-id")
	}
	config, err := godotenv.Read(".env")
	if err != nil {
		log.Fatal("Cannot read local database configuration")
	}
	db, err := postgres.NewPostgresDB(config["DATABASE_URL"])
	if err != nil {
		log.Fatal("Cannot connect to database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT id::text,username FROM users WHERE lower(email)=lower($1)`, *email)
	if err != nil {
		log.Fatal(err)
	}
	var userID, username string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&userID, &username); err != nil {
			log.Fatal(err)
		}
		count++
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		log.Fatal(rowErr)
	}
	if count != 1 || (*expected != "" && userID != *expected) {
		log.Fatal("Refusing grant: account does not match the verified unique user")
	}
	ids := strings.Split(*events, ",")
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
	}
	for _, id := range ids {
		var title string
		var date time.Time
		if err := db.QueryRowContext(ctx, `SELECT title,date FROM events WHERE id=$1`, id).Scan(&title, &date); err != nil {
			log.Fatal("Refusing grant: event does not exist or is invalid")
		}
		fmt.Printf("Event: %s | %s\n", title, date.Format(time.RFC3339))
	}
	fmt.Printf("Verified account: %s (%s), requested badges: %d\n", username, userID, len(ids))
	if !*apply {
		fmt.Println("Read-only preview; no changes applied")
		return
	}
	badges := postgres.NewBadgeRepository(db)
	if err := badges.InitSchema(ctx); err != nil {
		log.Fatal(err)
	}
	// Do not impose a new FK on the still-running older live API.
	if err := postgres.NewEventSurveyRepository(db).InitSchemaCompatible(ctx); err != nil {
		log.Fatal(err)
	}
	inserted, err := badges.GrantManual(ctx, userID, ids, *reason)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Assigned %d manual badges; existing badges, steps and reviews preserved\n", inserted)
}
