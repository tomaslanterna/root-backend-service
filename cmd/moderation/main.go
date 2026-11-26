// moderation revisa y resuelve las denuncias de contenido (tabla content_reports). Cada denuncia nueva también
// llega por email a SUPPORT_EMAIL. Apple espera que se actúe en menos de 24 h.
//
//	go run ./cmd/moderation list [--status pending|actioned|dismissed|all] [--limit 50]
//	go run ./cmd/moderation show    --id <report-uuid>
//	go run ./cmd/moderation dismiss --id <report-uuid>            # no infringe nada
//	go run ./cmd/moderation remove  --id <report-uuid>            # oculta el post o borra el texto del comentario/mensaje
//	go run ./cmd/moderation suspend --user <user-uuid|username> [--id <report-uuid>]   # la cuenta no puede volver a entrar
//
// Usa DATABASE_URL.
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

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

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

	if err := run(ctx, os.Args[1:], postgres.NewModerationRepository(db), postgres.NewUserRepository(db), os.Stdout); err != nil {
		log.Fatalf("❌ moderation: %v", err)
	}
}

const usage = "usage: moderation list|show|dismiss|remove|suspend [--id <report-uuid>] [--user <uuid|username>] [--status pending] [--limit 50]"

func run(ctx context.Context, args []string, reports ports.ReportRepository, users userLookup, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	command := args[0]
	fs := flag.NewFlagSet("moderation "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "report id (uuid)")
	user := fs.String("user", "", "user id (uuid) or username")
	status := fs.String("status", domain.ReportStatusPending, "pending|actioned|dismissed|all")
	limit := fs.Int("limit", 50, "max reports to list")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q\n%s", fs.Args(), usage)
	}
	reportID := strings.TrimSpace(*id)

	loadReport := func() (*domain.ContentReport, error) {
		if _, err := uuid.Parse(reportID); err != nil {
			return nil, errors.New("--id must be a report uuid\n" + usage)
		}
		return reports.GetReport(ctx, reportID)
	}

	switch command {
	case "list":
		filter := strings.TrimSpace(*status)
		if filter == "all" {
			filter = ""
		}
		if *limit < 1 || *limit > 500 {
			return errors.New("--limit must be between 1 and 500")
		}
		list, err := reports.ListReports(ctx, filter, *limit)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "· no reports")
		}
		for _, r := range list {
			fmt.Fprintf(out, "%s  %-9s  %-7s  %-5s  @%-20s  %s\n", r.ID, r.Status, r.TargetType, r.Reason, r.TargetUsername, preview(r.ContentSnapshot, 60))
		}
	case "show":
		r, err := loadReport()
		if err != nil {
			return err
		}
		printReport(out, r)
	case "dismiss":
		if _, err := loadReport(); err != nil {
			return err
		}
		if err := reports.ResolveReport(ctx, reportID, domain.ReportStatusDismissed); err != nil {
			return err
		}
		fmt.Fprintf(out, "✅ report %s dismissed\n", reportID)
	case "remove":
		r, err := loadReport()
		if err != nil {
			return err
		}
		if r.TargetType == domain.ReportTargetUser {
			return errors.New("a user report has no content to remove; use: moderation suspend --user " + r.TargetID + " --id " + r.ID)
		}
		if err := reports.RemoveReportedContent(ctx, *r); err != nil {
			return err
		}
		if err := reports.ResolveReport(ctx, reportID, domain.ReportStatusActioned); err != nil {
			return err
		}
		fmt.Fprintf(out, "✅ %s %s removed; report %s actioned\n", r.TargetType, r.TargetID, reportID)
	case "suspend":
		target := strings.TrimSpace(*user)
		if target == "" {
			return errors.New("--user is required\n" + usage)
		}
		if _, err := uuid.Parse(target); err != nil {
			u, err := users.GetUserByUsername(ctx, strings.ToLower(strings.TrimPrefix(target, "@")))
			if err != nil {
				return fmt.Errorf("looking up %q: %w", target, err)
			}
			target = u.ID
		}
		if err := reports.SuspendUser(ctx, target); err != nil {
			return err
		}
		fmt.Fprintf(out, "✅ user %s suspended (can no longer sign in)\n", target)
		if reportID != "" {
			if err := reports.ResolveReport(ctx, reportID, domain.ReportStatusActioned); err != nil {
				return err
			}
			fmt.Fprintf(out, "✅ report %s actioned\n", reportID)
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}
	return nil
}

func preview(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

func printReport(out io.Writer, r *domain.ContentReport) {
	value := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	reporter := "-"
	if r.ReporterUsername != "" {
		reporter = "@" + r.ReporterUsername
	}
	author := "-"
	if r.TargetUsername != "" {
		author = "@" + r.TargetUsername
	}
	fmt.Fprintf(out, "Report:      %s\nStatus:      %s\nCreated:     %s\nType:        %s %s\nAuthor:      %s\nReported by: %s\nReason:      %s\nDetails:     %s\nContent:\n%s\n",
		r.ID, r.Status, r.CreatedAt.Format("2006-01-02 15:04"), r.TargetType, r.TargetID, author, reporter, r.Reason, value(r.Details), value(r.ContentSnapshot))
	if r.Status == domain.ReportStatusPending {
		fmt.Fprintf(out, "\nNext: moderation dismiss --id %s | moderation remove --id %s", r.ID, r.ID)
		if r.TargetUserID != nil {
			fmt.Fprintf(out, " | moderation suspend --user %s --id %s", *r.TargetUserID, r.ID)
		}
		fmt.Fprintln(out)
	}
}
