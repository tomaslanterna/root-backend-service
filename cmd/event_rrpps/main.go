// event_rrpps asigna, quita o lista los RRPPs oficiales de un evento (tabla event_rrpps). Es la única forma de
// asignarlos: no hay endpoint HTTP. Solo se puede asignar a usuarios con users.role = 'RRPP'.
//
//	go run ./cmd/event_rrpps assign --event <event-uuid> --user <user-uuid> [--position 1]
//	go run ./cmd/event_rrpps assign --event <event-uuid> --username valesosa --position 2
//	go run ./cmd/event_rrpps remove --event <event-uuid> --user <user-uuid>
//	go run ./cmd/event_rrpps list   --event <event-uuid>
//
// Volver a asignar a alguien que ya está solo cambia su position. Usa DATABASE_URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"

	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	coreServices "root-backend-service/internal/core/services"
)

// userLookup resuelve --username a un id. Solo se usa el ID del usuario devuelto.
type userLookup interface {
	GetUserByUsername(ctx context.Context, username string) (*domain.User, error)
}

func main() {
	_ = godotenv.Overload()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.NewPostgresDB(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("❌ PostgreSQL: %v", err)
	}
	defer db.Close()

	repo := postgres.NewEventRRPPRepository(db)
	if err := run(ctx, os.Args[1:], coreServices.NewEventRRPPService(repo), postgres.NewUserRepository(db), os.Stdout); err != nil {
		log.Fatalf("❌ event_rrpps: %v", err)
	}
}

const usage = "usage: event_rrpps assign|remove|list --event <uuid> [--user <uuid> | --username <alias>] [--position N]"

func run(ctx context.Context, args []string, svc ports.EventRRPPService, users userLookup, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	command := args[0]
	fs := flag.NewFlagSet("event_rrpps "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventID := fs.String("event", "", "event id (uuid)")
	userID := fs.String("user", "", "user id (uuid)")
	username := fs.String("username", "", "username, instead of --user")
	position := fs.Int("position", 0, "display order (ascending, 0..32767)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q\n%s", fs.Args(), usage)
	}
	if strings.TrimSpace(*eventID) == "" {
		return errors.New("--event is required\n" + usage)
	}
	event := strings.TrimSpace(*eventID)

	resolveUser := func() (string, error) {
		id, alias := strings.TrimSpace(*userID), strings.ToLower(strings.TrimSpace(*username))
		switch {
		case id != "" && alias != "":
			return "", errors.New("use --user or --username, not both")
		case id != "":
			return id, nil
		case alias != "":
			u, err := users.GetUserByUsername(ctx, alias)
			if err != nil {
				return "", fmt.Errorf("looking up username %q: %w", alias, err)
			}
			return u.ID, nil
		default:
			return "", errors.New("--user or --username is required\n" + usage)
		}
	}

	switch command {
	case "assign":
		user, err := resolveUser()
		if err != nil {
			return err
		}
		if err := svc.Assign(ctx, event, user, *position); err != nil {
			return fmt.Errorf("assigning rrpp %s to event %s: %w", user, event, err)
		}
		fmt.Fprintf(out, "✅ RRPP %s assigned to event %s (position %d)\n", user, event, *position)
	case "remove":
		user, err := resolveUser()
		if err != nil {
			return err
		}
		removed, err := svc.Remove(ctx, event, user)
		if err != nil {
			return fmt.Errorf("removing rrpp %s from event %s: %w", user, event, err)
		}
		if removed {
			fmt.Fprintf(out, "✅ RRPP %s removed from event %s\n", user, event)
		} else {
			fmt.Fprintf(out, "· RRPP %s was not assigned to event %s\n", user, event)
		}
	case "list":
		rrpps, err := svc.ListForEvent(ctx, event)
		if err != nil {
			return fmt.Errorf("listing rrpps of event %s: %w", event, err)
		}
		if len(rrpps) == 0 {
			fmt.Fprintf(out, "· event %s has no RRPPs\n", event)
		}
		for _, r := range rrpps {
			fmt.Fprintf(out, "%5d  %s  @%s  %s\n", r.Position, r.UserID, r.Username, r.Name)
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}
	return nil
}
