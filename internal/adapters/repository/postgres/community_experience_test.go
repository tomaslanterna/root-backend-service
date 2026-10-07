package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	communityService "root-backend-service/internal/services/community"
	"strings"
	"testing"
	"time"
)

// The opt-in integration creates and drops ONLY its randomly named schema.
// Public users, communities, announcements and memberships are never modified.
func TestCommunityExperiencePostgresIntegration(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("COMMUNITY_TEST_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dsn = values["DATABASE_URL"]
	}
	if dsn == "" {
		t.Skip("set COMMUNITY_TEST_DATABASE_URL or COMMUNITY_TEST_USE_LOCAL_ENV=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "community_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("isolated schema cleanup: %v", err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	params := parsed.Query()
	params.Set("search_path", schema+",public")
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pq.QuoteIdentifier(schema)+`, public`); err != nil {
		t.Fatal(err)
	}
	var actual string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&actual); err != nil || actual != schema {
		t.Fatalf("refusing fixtures outside isolated schema: %q (%v)", actual, err)
	}
	ddl := []string{
		`CREATE TABLE users(id UUID PRIMARY KEY,name TEXT,username TEXT,role TEXT,avatar_url TEXT,is_kyc_verified BOOLEAN)`,
		`CREATE TABLE communities(id UUID PRIMARY KEY,name TEXT,pr_owner_id UUID REFERENCES users(id),country_id TEXT,cover_image_url TEXT,description TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE community_members(community_id UUID REFERENCES communities(id),user_id UUID REFERENCES users(id),joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE posts(id UUID PRIMARY KEY,author_id UUID REFERENCES users(id),community_id UUID REFERENCES communities(id),event_id UUID,title TEXT,content TEXT,long_content TEXT,header_image_url TEXT,timestamp TIMESTAMPTZ NOT NULL,is_featured BOOLEAN NOT NULL DEFAULT FALSE)`,
		`CREATE TABLE comments(id UUID PRIMARY KEY,target_type TEXT,target_id UUID,author_id UUID,content TEXT,timestamp TIMESTAMPTZ)`,
	}
	for _, statement := range ddl {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewCommunityRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("idempotent schema: %v", err)
	}
	service := communityService.NewCommunityService(repo)
	member, owner, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, u := range []struct{ id, role string }{{member, "USER"}, {owner, "RRPP"}, {outsider, "USER"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users(id,name,username,role) VALUES($1,'Fixture','fixture',$2)`, u.id, u.role); err != nil {
			t.Fatal(err)
		}
	}
	id, other := uuid.NewString(), uuid.NewString()
	for _, c := range []struct{ id, slug string }{{id, "fixture"}, {other, "other"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO communities(id,name,slug,category,pr_owner_id,country_id,is_active) VALUES($1,'Fixture',$2,'electrónica',$3,'UY',TRUE)`, c.id, c.slug, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.JoinCommunity(ctx, id, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE community_members SET joined_at=NOW()-INTERVAL '2 days' WHERE user_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	a, b := "10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002"
	for _, post := range []string{a, b} {
		if _, err := db.ExecContext(ctx, `INSERT INTO posts(id,author_id,community_id,content,timestamp) VALUES($1,$2,$3,'Announcement',CURRENT_DATE)`, post, owner, id); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := service.GetCommunity(ctx, id, member)
	if err != nil {
		t.Fatal(err)
	}
	if detail.UnreadCount != 2 || detail.Contact == nil || detail.Contact.ID != owner {
		t.Fatalf("hydration: %+v", detail)
	}
	mine, total, err := service.GetCommunities(ctx, domain.CommunityFilter{Scope: "mine", Limit: 1}, member)
	if err != nil || total != 1 || len(mine) != 1 || mine[0].ID != id {
		t.Fatalf("mine pagination: %v %d %v", mine, total, err)
	}
	explore, _, err := service.GetCommunities(ctx, domain.CommunityFilter{Scope: "explore", Limit: 50}, member)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range explore {
		if c.ID == id {
			t.Fatal("joined community leaked into explore")
		}
	}
	if err := service.SetMuted(ctx, id, member, true); err != nil {
		t.Fatal(err)
	}
	detail, err = service.GetCommunity(ctx, id, member)
	if err != nil || !detail.Muted || !detail.IsMember || detail.UnreadCount != 2 {
		t.Fatalf("mute must persist independently of membership/read: %+v %v", detail, err)
	}
	if err := service.SetMuted(ctx, id, outsider, true); !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("outsider preferences: %v", err)
	}
	if err := service.MarkRead(ctx, id, member, a); err != nil {
		t.Fatal(err)
	}
	detail, err = service.GetCommunity(ctx, id, member)
	if err != nil || detail.UnreadCount != 1 {
		t.Fatalf("timestamp tie: %+v %v", detail, err)
	}
	if err := service.MarkRead(ctx, id, member, b); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkRead(ctx, id, member, a); err != nil {
		t.Fatal(err)
	}
	detail, err = service.GetCommunity(ctx, id, member)
	if err != nil || detail.UnreadCount != 0 {
		t.Fatalf("read cannot regress: %+v %v", detail, err)
	}
	if err := service.SetPinned(ctx, id, outsider, a, true); !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("unauthorized pin: %v", err)
	}
	if err := service.SetPinned(ctx, id, owner, a, true); err != nil {
		t.Fatal(err)
	}
	posts, _, err := NewPostRepository(db).GetCommunityPosts(ctx, id, 1, 0)
	if err != nil || len(posts) != 1 || posts[0].ID != a || !posts[0].IsPinned {
		t.Fatalf("pinned ordering: %+v %v", posts, err)
	}
	if err := service.SetPinned(ctx, other, owner, a, true); !errors.Is(err, domain.ErrCommunityTargetNotFound) {
		t.Fatalf("cross-community pin: %v", err)
	}
	input := domain.CommunityReportInput{TargetType: "post", TargetID: a, Reason: "spam", Details: "  fixture report  "}
	reportID, err := service.CreateReport(ctx, id, member, input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.CreateReport(ctx, id, member, input)
	if err != nil || duplicate != reportID {
		t.Fatalf("duplicate report: %s %v", duplicate, err)
	}
	if _, err := service.CreateReport(ctx, other, member, input); !errors.Is(err, domain.ErrCommunityTargetNotFound) {
		t.Fatalf("cross-community report: %v", err)
	}
	commentID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO comments VALUES($1,'post',$2,$3,'Comment',NOW())`, commentID, a, member); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateReport(ctx, id, outsider, domain.CommunityReportInput{TargetType: "comment", TargetID: commentID, Reason: "abuse"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GetReports(ctx, id, member, 10, 0); !errors.Is(err, domain.ErrCommunityForbidden) {
		t.Fatalf("private reports: %v", err)
	}
	reports, total, err := service.GetReports(ctx, id, owner, 1, 0)
	if err != nil || total != 2 || len(reports) != 1 {
		t.Fatalf("report pagination: %+v %d %v", reports, total, err)
	}
	if err := service.ReviewReport(ctx, id, owner, reportID, "reviewed"); err != nil {
		t.Fatal(err)
	}
	_, total, err = service.GetReports(ctx, id, owner, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("review persists: %d %v", total, err)
	}
	if err := service.ReviewReport(ctx, other, owner, reportID, "dismissed"); !errors.Is(err, domain.ErrCommunityTargetNotFound) {
		t.Fatalf("cross-community review: %v", err)
	}
	if _, err := service.LeaveCommunity(ctx, id, member); err != nil {
		t.Fatal(err)
	}
	_, total, err = service.GetCommunities(ctx, domain.CommunityFilter{Scope: "mine", Limit: 12}, member)
	if err != nil || total != 0 {
		t.Fatalf("leave/mine: %d %v", total, err)
	}
}
