package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"root-backend-service/internal/core/domain"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedSQL struct {
	mu      sync.Mutex
	queries []string
	execs   []string
}

func (s *recordedSQL) addQuery(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, strings.Join(strings.Fields(query), " "))
}

func (s *recordedSQL) addExec(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execs = append(s.execs, strings.Join(strings.Fields(query), " "))
}

type eventTestDriver struct{ state *recordedSQL }

func (d *eventTestDriver) Open(string) (driver.Conn, error) {
	return &eventTestConn{state: d.state}, nil
}

type eventTestConn struct{ state *recordedSQL }

func (c *eventTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by the test driver")
}
func (c *eventTestConn) Close() error              { return nil }
func (c *eventTestConn) Begin() (driver.Tx, error) { return eventTestTx{}, nil }
func (c *eventTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return eventTestTx{}, nil
}

func (c *eventTestConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.addExec(query)
	return driver.RowsAffected(1), nil
}

func (c *eventTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.addQuery(query)
	normalized := strings.Join(strings.Fields(query), " ")
	now := time.Date(2026, time.September, 20, 2, 0, 0, 0, time.UTC)

	switch {
	case strings.Contains(normalized, "SELECT EXISTS(SELECT 1 FROM events"):
		return newEventTestRows([]string{"exists"}, []driver.Value{true}), nil
	case strings.HasPrefix(normalized, "SELECT COUNT(*) FROM events e"):
		return newEventTestRows([]string{"count"}, []driver.Value{int64(3)}), nil
	case strings.HasPrefix(normalized, "SELECT COUNT(*) FROM event_rsvps r"):
		return newEventTestRows([]string{"count"}, []driver.Value{int64(1)}), nil
	case strings.HasPrefix(normalized, "SELECT COUNT(*) FROM comments"):
		return newEventTestRows([]string{"count"}, []driver.Value{int64(1)}), nil
	case strings.Contains(normalized, "COUNT(*) FILTER (WHERE status = 'going')") && strings.Contains(normalized, "WHERE event_id::text = $1"):
		return newEventTestRows([]string{"going", "not_going"}, []driver.Value{int64(8), int64(3)}), nil
	case strings.HasPrefix(normalized, "SELECT e.id, e.title"):
		return newEventTestRows(
			[]string{"id", "title", "producer_id", "date", "location", "banner", "description", "lineup", "genre", "price", "featured", "created_at", "going", "not_going", "user_rsvp"},
			[]driver.Value{"event-1", "Fiesta", nil, now, "Montevideo", "https://image", "Descripción", "{DJ}", "Electrónica", float64(1200), true, now, int64(8), int64(3), "going"},
		), nil
	case strings.HasPrefix(normalized, "SELECT u.id, u.name"):
		return newEventTestRows(
			[]string{"id", "name", "username", "avatar", "verified"},
			[]driver.Value{"followed-1", "Persona seguida", "seguida", nil, true},
		), nil
	case strings.HasPrefix(normalized, "SELECT c.id, c.target_id"):
		return newEventTestRows(
			[]string{"id", "target", "author", "name", "username", "avatar", "content", "timestamp"},
			[]driver.Value{"comment-1", "event-1", "user-1", "Usuario", "usuario", nil, "Comentario", now},
		), nil
	case strings.HasPrefix(normalized, "INSERT INTO comments"):
		return newEventTestRows([]string{"id", "timestamp"}, []driver.Value{"comment-new", now}), nil
	case strings.HasPrefix(normalized, "SELECT name, username, avatar_url FROM users"):
		return newEventTestRows([]string{"name", "username", "avatar"}, []driver.Value{"Usuario", "usuario", nil}), nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", normalized)
	}
}

type eventTestTx struct{}

func (eventTestTx) Commit() error   { return nil }
func (eventTestTx) Rollback() error { return nil }

type eventTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func newEventTestRows(columns []string, row []driver.Value) *eventTestRows {
	return &eventTestRows{columns: columns, values: [][]driver.Value{row}}
}

func (r *eventTestRows) Columns() []string { return r.columns }
func (r *eventTestRows) Close() error      { return nil }
func (r *eventTestRows) Next(destination []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(destination, r.values[r.index])
	r.index++
	return nil
}

var eventTestDriverID atomic.Int64

func newEventRepositoryForTest(t *testing.T) (*EventRepository, *recordedSQL) {
	t.Helper()
	state := &recordedSQL{}
	name := fmt.Sprintf("event-test-driver-%d", eventTestDriverID.Add(1))
	sql.Register(name, &eventTestDriver{state: state})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &EventRepository{db: db}, state
}

func TestBuildEventWhereCombinesFiltersAndAlwaysExcludesPastEvents(t *testing.T) {
	minPrice, maxPrice, paid := 100.0, 500.0, false
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	where, args := buildEventWhere(domain.EventFilter{
		Genre: "Electrónica", Location: "Montevideo", MinPrice: &minPrice,
		MaxPrice: &maxPrice, IsFree: &paid, StartDate: &start, EndDate: &end, Query: "fiesta",
	}, 1)

	for _, expected := range []string{
		"e.date >= NOW()", "e.genre ILIKE $1", "e.location ILIKE $2",
		"e.price >= $3", "e.price <= $4", "e.price > 0",
		"e.date >= $5", "e.date <= $6", "e.title ILIKE $7",
	} {
		if !strings.Contains(where, expected) {
			t.Fatalf("expected %q in where clause: %s", expected, where)
		}
	}
	if len(args) != 7 {
		t.Fatalf("expected 7 arguments, got %d", len(args))
	}
}

func TestGetEventsReturnsRealTotalAndStableFutureOrder(t *testing.T) {
	repository, state := newEventRepositoryForTest(t)
	events, total, err := repository.GetEvents(context.Background(), domain.EventFilter{Limit: 12}, "user-1")
	if err != nil {
		t.Fatalf("getting events: %v", err)
	}
	if total != 3 || len(events) != 1 || events[0].GoingCount != 8 || events[0].Price == nil {
		t.Fatalf("unexpected event result: total=%d events=%+v", total, events)
	}
	queries := strings.Join(state.queries, "\n")
	if !strings.Contains(queries, "ORDER BY e.date ASC, e.id ASC") {
		t.Fatalf("stable date order missing from query: %s", queries)
	}
}

func TestRSVPUsesUpsertAndReturnsUpdatedCounts(t *testing.T) {
	repository, state := newEventRepositoryForTest(t)
	going, notGoing, status, err := repository.RSVPEvent(context.Background(), "user-1", "event-1", "not_going")
	if err != nil {
		t.Fatalf("upserting rsvp: %v", err)
	}
	if going != 8 || notGoing != 3 || status != "not_going" {
		t.Fatalf("unexpected rsvp result: %d %d %s", going, notGoing, status)
	}
	execs := strings.Join(state.execs, "\n")
	if !strings.Contains(execs, "ON CONFLICT (user_id, event_id) DO UPDATE") {
		t.Fatalf("upsert clause missing: %s", execs)
	}
}

func TestClearEventRSVPDeletesSelectionAndReturnsUpdatedCounts(t *testing.T) {
	repository, state := newEventRepositoryForTest(t)
	going, notGoing, err := repository.ClearEventRSVP(context.Background(), "user-1", "event-1")
	if err != nil {
		t.Fatalf("clearing rsvp: %v", err)
	}
	if going != 8 || notGoing != 3 {
		t.Fatalf("unexpected rsvp counts: %d %d", going, notGoing)
	}
	execs := strings.Join(state.execs, "\n")
	if !strings.Contains(execs, "DELETE FROM event_rsvps") ||
		!strings.Contains(execs, "event_id::text = $1 AND user_id::text = $2") {
		t.Fatalf("rsvp delete query missing: %s", execs)
	}
}

func TestFollowedAttendeesAreFilteredInSQL(t *testing.T) {
	repository, state := newEventRepositoryForTest(t)
	attendees, total, err := repository.GetFollowedGoingAttendees(context.Background(), "event-1", "user-1", 20, 0)
	if err != nil {
		t.Fatalf("getting followed attendees: %v", err)
	}
	if total != 1 || len(attendees) != 1 || attendees[0].ID != "followed-1" {
		t.Fatalf("unexpected attendees: total=%d data=%+v", total, attendees)
	}
	queries := strings.Join(state.queries, "\n")
	if !strings.Contains(queries, "JOIN users viewer") ||
		!strings.Contains(queries, "u.id::text = ANY(COALESCE(viewer.following") {
		t.Fatalf("follow restriction missing from SQL: %s", queries)
	}
	if strings.Contains(queries, "JOIN users current_user") {
		t.Fatalf("reserved current_user keyword must not be used as an alias: %s", queries)
	}
}

func TestEventCommentsArePaginatedNewestFirstAndCanBeCreated(t *testing.T) {
	repository, state := newEventRepositoryForTest(t)
	comments, total, err := repository.GetEventComments(context.Background(), "event-1", 20, 0)
	if err != nil {
		t.Fatalf("getting comments: %v", err)
	}
	if total != 1 || len(comments) != 1 || comments[0].Content != "Comentario" {
		t.Fatalf("unexpected comments: total=%d data=%+v", total, comments)
	}
	created, err := repository.CreateEventComment(context.Background(), "event-1", "user-1", "Nuevo")
	if err != nil {
		t.Fatalf("creating comment: %v", err)
	}
	if created.ID != "comment-new" || created.AuthorUsername != "usuario" {
		t.Fatalf("unexpected created comment: %+v", created)
	}
	queries := strings.Join(state.queries, "\n")
	if !strings.Contains(queries, "ORDER BY c.timestamp DESC, c.id DESC") {
		t.Fatalf("comment ordering missing: %s", queries)
	}
	if !strings.Contains(queries, "$1::uuid") || !strings.Contains(queries, "$2::uuid") {
		t.Fatalf("comment parameters must be cast consistently as UUIDs: %s", queries)
	}
}
