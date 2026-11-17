// import_events finds upcoming electronic-music events in Uruguay on the web
// and adds them to the events table. It runs once and exits, so schedule it
// with cron on the server, once a week (Mondays 06:00, Uruguay time):
//
//	CRON_TZ=America/Montevideo
//	0 6 * * 1 cd /path/to/root-backend-service && ./bin/import_events >> /var/log/import_events.log 2>&1
//
// Build it with: go build -o bin/import_events ./cmd/import_events
// Try it first with --dry-run, which verifies everything but writes nothing.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/ports"
	aiservice "root-backend-service/internal/services/ai"
	"root-backend-service/internal/services/eventimport"
	s3service "root-backend-service/internal/services/s3"
)

type s3Uploader struct{ s3 s3service.S3Service }

func (u s3Uploader) Upload(ctx context.Context, key, contentType string, data []byte) error {
	_, err := u.s3.UploadToS3(ctx, bytes.NewReader(data), key, contentType)
	return err
}

// ticketAdapter lets the importer reuse the app's existing Gemini ticket reader.
type ticketAdapter struct{ provider ports.AiTicketService }

func (t ticketAdapter) Fetch(ctx context.Context, title, date, location, country, ticketURL string) (string, []eventimport.Tier, error) {
	info, err := t.provider.FetchEventTickets(ctx, title, date, location, country, ticketURL)
	if err != nil {
		return "", nil, err
	}
	source := ""
	if info.Source != nil {
		source = *info.Source
	}
	tiers := make([]eventimport.Tier, 0, len(info.TicketTiers))
	for _, t := range info.TicketTiers {
		tiers = append(tiers, eventimport.Tier{Name: t.Name, Price: t.Price, Currency: t.Currency, SoldOut: t.SoldOut, FewRemaining: t.FewRemaining})
	}
	return source, tiers, nil
}

func main() {
	days := flag.Int("days", 60, "how many days ahead to search")
	maxCreate := flag.Int("max", 40, "most events to create in one run")
	dryRun := flag.Bool("dry-run", false, "verify everything but write nothing to the database or S3")
	allowNoFlyer := flag.Bool("allow-no-flyer", false, "create events even when no flyer could be found")
	allowNoCoords := flag.Bool("allow-no-coords", false, "create events even when the venue could not be located")
	regions := flag.String("regions", "", "comma-separated regions to search instead of the defaults, e.g. \"Montevideo\"")
	model := flag.String("model", "gemini-2.5-flash", "Gemini model used for search and extraction")
	flag.Parse()
	if err := godotenv.Overload(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *days, *maxCreate, *dryRun, *allowNoFlyer, *allowNoCoords, *model, *regions); err != nil {
		log.Fatalf("❌ import_events: %v", err)
	}
}

func run(ctx context.Context, days, maxCreate int, dryRun, allowNoFlyer, allowNoCoords bool, model, regions string) error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY no configurada")
	}
	db, err := postgres.NewPostgresDB(os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("conectando a PostgreSQL: %w", err)
	}
	defer db.Close()
	store := &eventimport.SQLStore{DB: db}

	release, ok, err := store.Lock(ctx)
	if err != nil {
		return fmt.Errorf("tomando el lock: %w", err)
	}
	if !ok {
		log.Println("otra importación está en curso; salgo sin hacer nada")
		return nil
	}
	defer release()

	web := eventimport.NewWeb()
	discoverer, err := eventimport.NewGeminiDiscoverer(ctx, apiKey, model, web)
	if err != nil {
		return err
	}
	if regions != "" {
		discoverer.Regions = strings.Split(regions, ",")
	}
	imp := &eventimport.Importer{
		Discoverer: discoverer,
		Finder:     discoverer,
		Geocoder:   eventimport.NewNominatim(),
		Pages:      web,
		Tickets:    ticketAdapter{aiservice.NewGeminiTicketProvider()},
		Store:      store,
		Options:    eventimport.Options{DryRun: dryRun, AllowNoFlyer: allowNoFlyer, AllowNoCoords: allowNoCoords, Max: maxCreate},
	}
	if !dryRun {
		s3, err := s3service.NewS3Service(ctx)
		if err != nil {
			return fmt.Errorf("configurando S3: %w", err)
		}
		imp.Uploader = s3Uploader{s3}
	}

	today := time.Now().In(eventimport.Montevideo)
	from := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	window := eventimport.Window{From: from, To: from.AddDate(0, 0, days)}
	log.Printf("buscando eventos entre %s y %s (dry-run=%v)", window.From.Format("2006-01-02"), window.To.Format("2006-01-02"), dryRun)

	results, err := imp.Run(ctx, window)
	if err != nil {
		return err
	}
	created := 0
	for _, r := range results {
		switch {
		case r.Created:
			created++
			fmt.Printf("✔ %s  %-40.40s  %s  [%s]\n      %s\n", r.Date.Format("2006-01-02 15:04"), r.Title, r.Venue, r.ID, r.Detail)
		default:
			when := "                "
			if !r.Date.IsZero() {
				when = r.Date.Format("2006-01-02 15:04")
			}
			fmt.Printf("✖ %s  %-40.40s  %s  → %s\n", when, r.Title, r.Venue, r.Reason)
		}
	}
	fmt.Printf("\n%d creados, %d omitidos (de %d candidatos)\n", created, len(results)-created, len(results))
	return nil
}
