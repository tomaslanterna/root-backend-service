package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Afters: tablas `afters`, `after_attendees` y `after_reports` (contrato en AFTERS.md). El archivado se resuelve
// filtrando por fecha en cada consulta: no hay cron. La dirección (afters.address) solo se lee en Detail.

type AfterRepository struct{ db *sql.DB }

var _ ports.AfterRepository = (*AfterRepository)(nil)

func NewAfterRepository(db *sql.DB) *AfterRepository { return &AfterRepository{db: db} }

func hoursInterval(d time.Duration) string {
	return fmt.Sprintf("INTERVAL '%d hours'", int(d.Hours()))
}

var (
	// Fin efectivo: endsAt o, si no hay, 12 h después de empezar.
	afterEndExpr = `COALESCE(a.ends_at, a.starts_at + ` + hoursInterval(domain.AfterDefaultDuration) + `)`
	// Se archiva a las 24 h de empezar.
	afterArchiveExpr = `(a.starts_at + ` + hoursInterval(domain.AfterArchiveAfter) + `)`
	// Próximo o en curso (y no archivado), con status active.
	afterUpcomingExpr = `(a.status = 'active' AND NOW() < ` + afterEndExpr + ` AND NOW() < ` + afterArchiveExpr + `)`
	// Oficial: atado a un evento y subido por su organizador (events.producer_id) o por un RRPP del evento
	// (event_rrpps, con el rol RRPP vigente, igual que la lista de RRPPs del detalle).
	afterOfficialExpr = `(a.event_id IS NOT NULL AND ev.id IS NOT NULL AND (ev.producer_id = a.host_user_id OR EXISTS (
  SELECT 1 FROM event_rrpps er JOIN users ru ON ru.id = er.user_id
  WHERE er.event_id = a.event_id AND er.user_id = a.host_user_id AND UPPER(COALESCE(ru.role, '')) = 'RRPP')))`
)

// afterSummaryJSON arma el AfterSummary en SQL (host y evento en la misma consulta: sin N+1). `viewer` es un
// placeholder uuid (NULL sin sesión). coverUrl e image salen como claves S3 y el handler las firma.
func afterSummaryJSON(viewer string) string {
	return `jsonb_build_object(
 'id', a.id, 'name', a.name, 'coverUrl', NULLIF(a.cover_key, ''),
 'startsAt', a.starts_at, 'endsAt', a.ends_at, 'archivesAt', ` + afterArchiveExpr + `,
 'zone', a.zone, 'access', a.access, 'price', a.price_text, 'ticketUrl', a.ticket_url, 'capacity', a.capacity,
 'acceptedCount', (SELECT COUNT(*) FROM after_attendees c WHERE c.after_id = a.id AND c.status = 'accepted'),
 'goingCount', (SELECT COUNT(*) FROM after_attendees c WHERE c.after_id = a.id AND c.status = 'going'),
 'isOfficial', ` + afterOfficialExpr + `,
 'host', jsonb_build_object('userId', hu.id, 'name', COALESCE(hu.name, ''), 'username', COALESCE(hu.username, ''),
   'avatarUrl', NULLIF(hu.avatar_url, ''), 'isKycVerified', COALESCE(hu.is_kyc_verified, FALSE)),
 'event', CASE WHEN ev.id IS NULL THEN NULL ELSE jsonb_build_object('id', ev.id, 'slug', ev.slug, 'title', ev.title,
   -- Mismo formato que el resto de los eventos: la hora de Montevideo con "Z" (el front la corrige con parseEventRealDate).
   'date', (ev.date AT TIME ZONE 'UTC'), 'location', ev.location, 'image', NULLIF(ev.cinematic_banner_url, '')) END,
 'genres', to_jsonb(COALESCE(a.genres, '{}'::text[])),
 'viewerStatus', CASE WHEN ` + viewer + ` IS NOT NULL AND a.host_user_id = ` + viewer + ` THEN 'host'
   WHEN va.status IN ('requested', 'accepted', 'rejected', 'going') THEN va.status END)`
}

// afterFrom: el host tiene que tener la cuenta activa y no puede haber un bloqueo con quien mira (en ningún sentido).
// Termina en un WHERE al que se le anexan condiciones con AND.
func afterFrom(viewer string) string {
	return `
FROM afters a
JOIN users hu ON hu.id = a.host_user_id AND hu.deleted_at IS NULL AND hu.suspended_at IS NULL
LEFT JOIN events ev ON ev.id = a.event_id
LEFT JOIN after_attendees va ON va.after_id = a.id AND va.user_id = ` + viewer + `
WHERE NOT EXISTS (SELECT 1 FROM user_blocks ub WHERE
 (ub.blocker_id = ` + viewer + ` AND ub.blocked_id = a.host_user_id) OR (ub.blocker_id = a.host_user_id AND ub.blocked_id = ` + viewer + `))`
}

// AfterEventEmbedSQL es la subconsulta del embed `afters` de GET /v1/events/:id. eventExpr es la columna del evento
// de la consulta de afuera (por ejemplo e.id) y viewer un placeholder uuid. Devuelve un jsonb con total, preview
// (oficial primero y después por startsAt) y viewerAfterIds.
func AfterEventEmbedSQL(eventExpr, viewer string) string {
	return `(SELECT jsonb_build_object(
 'total', COUNT(*),
 'preview', COALESCE(jsonb_agg(x.item ORDER BY x.rn) FILTER (WHERE x.rn <= ` + fmt.Sprint(domain.AfterEventPreview) + `), '[]'::jsonb),
 'viewerAfterIds', COALESCE((SELECT jsonb_agg(a2.id ORDER BY a2.starts_at, a2.id) FROM afters a2
   WHERE a2.event_id = ` + eventExpr + ` AND a2.status = 'active'
   AND NOW() < COALESCE(a2.ends_at, a2.starts_at + ` + hoursInterval(domain.AfterDefaultDuration) + `)
   AND ` + viewer + ` IS NOT NULL AND (a2.host_user_id = ` + viewer + ` OR EXISTS (SELECT 1 FROM after_attendees t
     WHERE t.after_id = a2.id AND t.user_id = ` + viewer + ` AND t.status IN ('requested', 'accepted', 'going')))), '[]'::jsonb))
 FROM (SELECT ` + afterSummaryJSON(viewer) + ` AS item,
   ROW_NUMBER() OVER (ORDER BY ` + afterOfficialExpr + ` DESC, a.starts_at, a.id) AS rn` + afterFrom(viewer) + `
   AND a.event_id = ` + eventExpr + ` AND ` + afterUpcomingExpr + `) x)`
}

func (r *AfterRepository) InitSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning afters schema migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_afters_schema', 0))`,
		`CREATE TABLE IF NOT EXISTS afters (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 host_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 event_id UUID NULL REFERENCES events(id) ON DELETE SET NULL,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
 zone TEXT NOT NULL CHECK (char_length(zone) BETWEEN 1 AND 60),
 address TEXT NOT NULL CHECK (char_length(address) BETWEEN 1 AND 200),
 access TEXT NOT NULL CHECK (access IN ('free', 'approval', 'ticket')),
 price_text TEXT NULL CHECK (char_length(price_text) <= 40),
 ticket_url TEXT NULL CHECK (char_length(ticket_url) <= 500),
 capacity INTEGER NULL CHECK (capacity BETWEEN 2 AND 500),
 genres TEXT[] NOT NULL DEFAULT '{}',
 lineup TEXT NULL CHECK (char_length(lineup) <= 120),
 rules TEXT NULL CHECK (char_length(rules) <= 500),
 cover_key TEXT NULL,
 notice TEXT NULL CHECK (char_length(notice) <= 280),
 notice_updated_at TIMESTAMPTZ NULL,
 starts_at TIMESTAMPTZ NOT NULL,
 ends_at TIMESTAMPTZ NULL,
 status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'hidden', 'removed')),
 reviewed_at TIMESTAMPTZ NULL,
 reviewed_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CONSTRAINT afters_ends_after_start CHECK (ends_at IS NULL OR ends_at > starts_at),
 CONSTRAINT afters_ticket_needs_url CHECK (access <> 'ticket' OR ticket_url IS NOT NULL))`,
		`CREATE INDEX IF NOT EXISTS afters_event_starts_active_idx ON afters (event_id, starts_at) WHERE status = 'active'`,
		`CREATE INDEX IF NOT EXISTS afters_starts_active_idx ON afters (starts_at) WHERE status = 'active'`,
		`CREATE INDEX IF NOT EXISTS afters_host_idx ON afters (host_user_id)`,
		// FKs: el índice parcial de arriba solo cubre los activos.
		`CREATE INDEX IF NOT EXISTS afters_event_fk_idx ON afters (event_id) WHERE event_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS afters_reviewed_by_idx ON afters (reviewed_by) WHERE reviewed_by IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS afters_moderation_idx ON afters (status, updated_at DESC) WHERE status <> 'active'`,
		`CREATE TABLE IF NOT EXISTS after_attendees (
 after_id UUID NOT NULL REFERENCES afters(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 status TEXT NOT NULL CHECK (status IN ('requested', 'accepted', 'rejected', 'going', 'cancelled')),
 message TEXT NULL CHECK (char_length(message) <= 280),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (after_id, user_id))`,
		`CREATE INDEX IF NOT EXISTS after_attendees_after_status_idx ON after_attendees (after_id, status)`,
		`CREATE INDEX IF NOT EXISTS after_attendees_user_idx ON after_attendees (user_id)`,
		`CREATE TABLE IF NOT EXISTS after_reports (
 after_id UUID NOT NULL REFERENCES afters(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 280),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (after_id, user_id))`,
		`CREATE INDEX IF NOT EXISTS after_reports_user_idx ON after_reports (user_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("afters migration: %w", err)
		}
	}
	return tx.Commit()
}

// SchemaReady dice si las tablas de afters existen (para arrancar con -initialize-schema=false).
func (r *AfterRepository) SchemaReady(ctx context.Context) (bool, error) {
	var ready bool
	err := r.db.QueryRowContext(ctx, `SELECT to_regclass('afters') IS NOT NULL AND to_regclass('after_attendees') IS NOT NULL
 AND to_regclass('after_reports') IS NOT NULL AND to_regclass('event_rrpps') IS NOT NULL`).Scan(&ready)
	return ready, err
}

// ---------------------------------------------------------------- lecturas

func decodeAfterSummary(raw []byte) (domain.AfterSummary, error) {
	var s domain.AfterSummary
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("decoding after summary: %w", err)
	}
	if s.Genres == nil {
		s.Genres = []string{}
	}
	return s, nil
}

// queryAfterPage corre `SELECT COUNT(*) OVER(), <summary> <from> AND <where> ORDER BY <order>` paginado.
func (r *AfterRepository) queryAfterPage(ctx context.Context, where, order string, args []interface{}, limit, offset int) (domain.AfterPage, error) {
	page := domain.AfterPage{Data: []domain.AfterSummary{}, Meta: domain.AfterPageMeta{Limit: limit, Offset: offset}}
	n := len(args)
	q := `SELECT COUNT(*) OVER(), ` + afterSummaryJSON("$1::uuid") + afterFrom("$1::uuid") + ` AND ` + where +
		` ORDER BY ` + order + fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n+1, n+2)
	rows, err := r.db.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return page, fmt.Errorf("listing afters: %w", err)
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&total, &raw); err != nil {
			return page, fmt.Errorf("scanning after: %w", err)
		}
		item, err := decodeAfterSummary(raw)
		if err != nil {
			return page, err
		}
		page.Data = append(page.Data, item)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("iterating afters: %w", err)
	}
	// Una página vacía más allá del final no trae el total: se cuenta aparte.
	if len(page.Data) == 0 && offset > 0 {
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+afterFrom("$1::uuid")+` AND `+where, args...).Scan(&total); err != nil {
			return page, fmt.Errorf("counting afters: %w", err)
		}
	}
	page.Meta.Total = total
	page.Meta.HasMore = offset+len(page.Data) < total
	return page, nil
}

func (r *AfterRepository) ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.AfterPage, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1::uuid)`, eventID).Scan(&exists); err != nil {
		return domain.AfterPage{}, fmt.Errorf("checking event: %w", err)
	}
	if !exists {
		return domain.AfterPage{}, domain.ErrEventNotFound
	}
	return r.queryAfterPage(ctx, `a.event_id = $2::uuid AND `+afterUpcomingExpr,
		afterOfficialExpr+` DESC, a.starts_at, a.id`, []interface{}{nullUUID(viewerID), eventID}, limit, offset)
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// List: próximos o en curso que se solapan con [from, until]: terminan después de from y empiezan antes de until.
func (r *AfterRepository) List(ctx context.Context, filter domain.AfterFilter, viewerID string) (domain.AfterPage, error) {
	args := []interface{}{nullUUID(viewerID), filter.From}
	where := afterUpcomingExpr + ` AND ` + afterEndExpr + ` > $2::timestamptz`
	if filter.Until != nil {
		args = append(args, *filter.Until)
		where += fmt.Sprintf(` AND a.starts_at <= $%d::timestamptz`, len(args))
	}
	if filter.Zone != "" {
		args = append(args, "%"+escapeLike(filter.Zone)+"%")
		where += fmt.Sprintf(` AND a.zone ILIKE $%d`, len(args))
	}
	return r.queryAfterPage(ctx, where, `a.starts_at, a.id`, args, filter.Limit, filter.Offset)
}

func (r *AfterRepository) Detail(ctx context.Context, afterID, viewerID string) (*domain.AfterDetailRow, error) {
	viewer := "$1::uuid"
	q := `SELECT ` + afterSummaryJSON(viewer) + `, a.address, a.notice, a.notice_updated_at, a.status, a.rules, a.lineup,
 NOW() >= ` + afterArchiveExpr + `, NOW() >= ` + afterEndExpr + `,
 (SELECT COUNT(*) FROM after_attendees f JOIN users fu ON fu.id = f.user_id
   WHERE f.after_id = a.id AND f.status IN ('accepted', 'going') AND fu.deleted_at IS NULL AND fu.suspended_at IS NULL
   AND f.user_id::text = ANY(COALESCE((SELECT vu.following FROM users vu WHERE vu.id = ` + viewer + `), '{}'::text[]))),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('userId', p.id, 'name', COALESCE(p.name, ''), 'username', COALESCE(p.username, ''),
   'avatarUrl', NULLIF(p.avatar_url, '')) ORDER BY p.updated_at DESC) FROM (
   SELECT fu.id, fu.name, fu.username, fu.avatar_url, f.updated_at FROM after_attendees f JOIN users fu ON fu.id = f.user_id
   WHERE f.after_id = a.id AND f.status IN ('accepted', 'going') AND fu.deleted_at IS NULL AND fu.suspended_at IS NULL
   AND f.user_id::text = ANY(COALESCE((SELECT vu.following FROM users vu WHERE vu.id = ` + viewer + `), '{}'::text[]))
   ORDER BY f.updated_at DESC LIMIT ` + fmt.Sprint(domain.AfterFollowedPreview) + `) p), '[]'::jsonb)` +
		afterFrom(viewer) + ` AND a.id = $2::uuid`
	var (
		raw, followed         []byte
		address, status       string
		notice, rules, lineup sql.NullString
		noticeAt              sql.NullTime
		archived, ended       bool
		followedCount         int
	)
	err := r.db.QueryRowContext(ctx, q, nullUUID(viewerID), afterID).Scan(&raw, &address, &notice, &noticeAt, &status,
		&rules, &lineup, &archived, &ended, &followedCount, &followed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrAfterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading after: %w", err)
	}
	summary, err := decodeAfterSummary(raw)
	if err != nil {
		return nil, err
	}
	row := &domain.AfterDetailRow{Address: address, StoredStatus: status, Archived: archived, Ended: ended}
	row.Detail.AfterSummary = summary
	row.Detail.FollowedGoing.Count = followedCount
	row.Detail.FollowedGoing.Preview = []domain.AfterPerson{}
	if err := json.Unmarshal(followed, &row.Detail.FollowedGoing.Preview); err != nil {
		return nil, fmt.Errorf("decoding followed going: %w", err)
	}
	if notice.Valid {
		row.Notice = &notice.String
	}
	if noticeAt.Valid {
		row.NoticeUpdatedAt = &noticeAt.Time
	}
	if rules.Valid {
		row.Detail.Rules = &rules.String
	}
	if lineup.Valid {
		row.Detail.Lineup = &lineup.String
	}
	return row, nil
}

func (r *AfterRepository) EventWindow(ctx context.Context, eventID string) (domain.AfterEventWindow, error) {
	window := domain.AfterEventWindow{ID: eventID}
	err := r.db.QueryRowContext(ctx, `SELECT e.date AT TIME ZONE 'America/Montevideo',
 COALESCE(e.end_date, (e.date AT TIME ZONE 'America/Montevideo') + `+hoursInterval(domain.EventDefaultDuration)+`)
 FROM events e WHERE e.id = $1::uuid`, eventID).Scan(&window.StartsAt, &window.EndsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return window, domain.ErrEventNotFound
	}
	if err != nil {
		return window, fmt.Errorf("reading event window: %w", err)
	}
	return window, nil
}

// ---------------------------------------------------------------- escrituras

// afterNotify inserta la notificación; el trigger de `notifications` encola el push (FCM y APNs) y los adapters mandan
// `data` tal cual, más el type. Nunca lleva la dirección ni el texto del aviso.
func afterNotify(ctx context.Context, tx *sql.Tx, userID, kind string, category domain.NotificationCategory, title, body, dedupe string, data map[string]string) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (id, user_id, type, category, title, body, data, dedupe_key, scheduled_at)
 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, NOW()) ON CONFLICT DO NOTHING`,
		uuid.NewString(), userID, kind, string(category), truncateRunes(title, 120), truncateRunes(body, 500), payload, dedupe); err != nil {
		return fmt.Errorf("inserting %s notification: %w", kind, err)
	}
	return nil
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func afterPushData(afterID string, extra map[string]string) map[string]string {
	data := map[string]string{"id": afterID, "after_id": afterID, "url": "/afters/" + afterID}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func nullableString(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func genresArray(genres []string) interface{} {
	if genres == nil {
		genres = []string{}
	}
	return pq.Array(genres)
}

func (r *AfterRepository) Create(ctx context.Context, hostID string, f domain.AfterFields, now time.Time) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin create after: %w", err)
	}
	defer tx.Rollback()
	// Serializa las creaciones del mismo host: dos pedidos simultáneos no pasan el tope de activos.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('root_after_host:' || $1::text, 0))`, hostID); err != nil {
		return "", fmt.Errorf("locking host afters: %w", err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM afters a WHERE a.host_user_id = $1::uuid AND a.status = 'active'
 AND $2::timestamptz < `+afterEndExpr, hostID, now).Scan(&active); err != nil {
		return "", fmt.Errorf("counting active afters: %w", err)
	}
	if active >= domain.AftersMaxActivePerHost {
		return "", domain.ErrTooManyActiveAfters
	}
	afterID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO afters (id, host_user_id, event_id, name, zone, address, access, price_text,
 ticket_url, capacity, genres, lineup, rules, cover_key, starts_at, ends_at, status, created_at, updated_at)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'active', NOW(), NOW())`,
		afterID, hostID, nullableString(f.EventID), f.Name, f.Zone, f.Address, f.Access, nullableString(f.Price),
		nullableString(f.TicketURL), nullableInt(f.Capacity), genresArray(f.Genres), nullableString(f.Lineup),
		nullableString(f.Rules), nullableString(f.CoverKey), f.StartsAt, nullableTime(f.EndsAt)); err != nil {
		return "", fmt.Errorf("inserting after: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit create after: %w", err)
	}
	return afterID, nil
}

func (r *AfterRepository) Current(ctx context.Context, afterID string, now time.Time) (domain.AfterCurrent, error) {
	var (
		cur                                                   domain.AfterCurrent
		eventID, price, ticketURL, lineup, rules, cover, note sql.NullString
		endsAt                                                sql.NullTime
		capacity                                              sql.NullInt64
		genres                                                pq.StringArray
	)
	f := &cur.Fields
	err := r.db.QueryRowContext(ctx, `SELECT a.host_user_id, a.status, $2::timestamptz >= `+afterArchiveExpr+`,
 a.name, a.event_id, a.starts_at, a.ends_at, a.zone, a.address, a.access, a.price_text, a.ticket_url, a.capacity,
 a.genres, a.lineup, a.rules, a.cover_key, a.notice
 FROM afters a WHERE a.id = $1::uuid AND a.status <> 'removed'`, afterID, now).Scan(&cur.HostID, &cur.StoredStatus, &cur.Archived,
		&f.Name, &eventID, &f.StartsAt, &endsAt, &f.Zone, &f.Address, &f.Access, &price, &ticketURL, &capacity,
		&genres, &lineup, &rules, &cover, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return cur, domain.ErrAfterNotFound
	}
	if err != nil {
		return cur, fmt.Errorf("reading after: %w", err)
	}
	str := func(v sql.NullString) *string {
		if !v.Valid {
			return nil
		}
		s := v.String
		return &s
	}
	f.EventID, f.Price, f.TicketURL, f.Lineup, f.Rules, f.CoverKey, cur.Notice = str(eventID), str(price), str(ticketURL), str(lineup), str(rules), str(cover), str(note)
	if endsAt.Valid {
		t := endsAt.Time
		f.EndsAt = &t
	}
	if capacity.Valid {
		c := int(capacity.Int64)
		f.Capacity = &c
	}
	f.Genres = []string(genres)
	return cur, nil
}

// lockAfter bloquea la fila del after (serializa cupo, decisiones y denuncias) y devuelve sus datos básicos.
type lockedAfter struct {
	hostID, status, access, name string
	capacity                     sql.NullInt64
	archived, ended              bool
	taken                        int
}

func lockAfter(ctx context.Context, tx *sql.Tx, afterID string) (lockedAfter, error) {
	var a lockedAfter
	err := tx.QueryRowContext(ctx, `SELECT a.host_user_id, a.status, a.access, a.name, a.capacity,
 NOW() >= `+afterArchiveExpr+`, NOW() >= `+afterEndExpr+`
 FROM afters a WHERE a.id = $1::uuid FOR UPDATE OF a`, afterID).Scan(&a.hostID, &a.status, &a.access, &a.name,
		&a.capacity, &a.archived, &a.ended)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.ErrAfterNotFound
	}
	if err != nil {
		return a, fmt.Errorf("locking after: %w", err)
	}
	// El cupo se cuenta en otra sentencia, ya con el lock: en READ COMMITTED una subconsulta de la sentencia que
	// esperó el lock vería la foto de antes de esperar y dejaría pasar de más.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM after_attendees WHERE after_id = $1::uuid AND status IN ('accepted', 'going')`,
		afterID).Scan(&a.taken); err != nil {
		return a, fmt.Errorf("counting after attendees: %w", err)
	}
	return a, nil
}

func (a lockedAfter) full() bool { return a.capacity.Valid && int64(a.taken) >= a.capacity.Int64 }

func (r *AfterRepository) Update(ctx context.Context, afterID, hostID string, f domain.AfterFields, notice domain.NoticeChange) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update after: %w", err)
	}
	defer tx.Rollback()
	locked, err := lockAfter(ctx, tx, afterID)
	if err != nil {
		return err
	}
	if locked.status == domain.AfterStatusRemoved {
		return domain.ErrAfterNotFound
	}
	if locked.hostID != hostID {
		return domain.ErrNotAfterHost
	}
	if f.Capacity != nil && locked.taken > *f.Capacity {
		return &domain.ValidationError{Message: "capacity cannot be lower than the people already in"}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE afters SET event_id = $2::uuid, name = $3, zone = $4, address = $5, access = $6,
 price_text = $7, ticket_url = $8, capacity = $9, genres = $10, lineup = $11, rules = $12, cover_key = $13,
 starts_at = $14, ends_at = $15, updated_at = NOW() WHERE id = $1::uuid`,
		afterID, nullableString(f.EventID), f.Name, f.Zone, f.Address, f.Access, nullableString(f.Price),
		nullableString(f.TicketURL), nullableInt(f.Capacity), genresArray(f.Genres), nullableString(f.Lineup),
		nullableString(f.Rules), nullableString(f.CoverKey), f.StartsAt, nullableTime(f.EndsAt)); err != nil {
		return fmt.Errorf("updating after: %w", err)
	}
	if notice.Changed {
		var stamp time.Time
		if err := tx.QueryRowContext(ctx, `UPDATE afters SET notice = $2,
 notice_updated_at = CASE WHEN $2::text IS NULL THEN NULL ELSE NOW() END WHERE id = $1::uuid RETURNING COALESCE(notice_updated_at, NOW())`,
			afterID, nullableString(notice.Value)).Scan(&stamp); err != nil {
			return fmt.Errorf("updating after notice: %w", err)
		}
		if notice.Value != nil {
			hostName, err := displayName(ctx, tx, hostID)
			if err != nil {
				return err
			}
			payload, err := json.Marshal(afterPushData(afterID, nil))
			if err != nil {
				return err
			}
			// Un aviso por persona y versión del aviso. El texto del aviso no va en el push (podría tener la dirección).
			if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (id, user_id, type, category, title, body, data, dedupe_key, scheduled_at)
 SELECT gen_random_uuid(), t.user_id, 'after.notice', $2, $3, $4, $5, $6, NOW()
 FROM after_attendees t JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL
 WHERE t.after_id = $1::uuid AND t.status IN ('accepted', 'going') ON CONFLICT DO NOTHING`,
				afterID, string(domain.NotificationEvents), truncateRunes("Aviso nuevo en "+f.Name, 120),
				truncateRunes(hostName+" dejó un aviso. Tocá para verlo.", 500), payload,
				fmt.Sprintf("after.notice:%s:%d", afterID, stamp.UnixNano())); err != nil {
				return fmt.Errorf("notifying after notice: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update after: %w", err)
	}
	return nil
}

func (r *AfterRepository) Remove(ctx context.Context, afterID, hostID string) error {
	var host, status string
	err := r.db.QueryRowContext(ctx, `SELECT host_user_id, status FROM afters WHERE id = $1::uuid`, afterID).Scan(&host, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrAfterNotFound
	}
	if err != nil {
		return fmt.Errorf("reading after: %w", err)
	}
	if host != hostID {
		return domain.ErrNotAfterHost
	}
	if status == domain.AfterStatusRemoved {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE afters SET status = 'removed', updated_at = NOW() WHERE id = $1::uuid AND host_user_id = $2::uuid`, afterID, hostID); err != nil {
		return fmt.Errorf("removing after: %w", err)
	}
	return nil
}

func blockedBetween(ctx context.Context, tx *sql.Tx, a, b string) (bool, error) {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_blocks WHERE (blocker_id = $1::uuid AND blocked_id = $2::uuid)
 OR (blocker_id = $2::uuid AND blocked_id = $1::uuid))`, a, b).Scan(&blocked)
	return blocked, err
}

func (r *AfterRepository) Join(ctx context.Context, afterID, userID string, message *string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin join after: %w", err)
	}
	defer tx.Rollback()
	// El lock de la fila del after serializa los "Voy" y las aceptaciones: el cupo no se pasa.
	a, err := lockAfter(ctx, tx, afterID)
	if err != nil {
		return "", err
	}
	if a.status != domain.AfterStatusActive || a.archived {
		return "", domain.ErrAfterNotFound
	}
	blocked, err := blockedBetween(ctx, tx, userID, a.hostID)
	if err != nil {
		return "", fmt.Errorf("checking blocks: %w", err)
	}
	if blocked {
		return "", domain.ErrAfterNotFound
	}
	if a.hostID == userID {
		return "", domain.ErrAlreadyJoined
	}
	if a.ended {
		return "", domain.ErrAfterEnded
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT status FROM after_attendees WHERE after_id = $1::uuid AND user_id = $2::uuid FOR UPDATE`, afterID, userID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("reading attendee: %w", err)
	}
	// Decisión: quien fue rechazado no puede volver a pedir (evita insistir); cancelled sí puede volver.
	if err == nil && current != domain.AttendeeCancelled {
		return "", domain.ErrAlreadyJoined
	}
	if a.full() {
		return "", domain.ErrAfterFull
	}
	status := domain.AttendeeGoing
	if a.access == domain.AfterAccessApproval {
		status = domain.AttendeeRequested
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO after_attendees (after_id, user_id, status, message, created_at, updated_at)
 VALUES ($1::uuid, $2::uuid, $3, $4, NOW(), NOW())
 ON CONFLICT (after_id, user_id) DO UPDATE SET status = EXCLUDED.status, message = EXCLUDED.message, created_at = NOW(), updated_at = NOW()`,
		afterID, userID, status, nullableString(message)); err != nil {
		return "", fmt.Errorf("joining after: %w", err)
	}
	if status == domain.AttendeeRequested {
		name, err := displayName(ctx, tx, userID)
		if err != nil {
			return "", err
		}
		// Un solo push por persona y after (cancelar y volver a pedir no vuelve a notificar).
		if err := afterNotify(ctx, tx, a.hostID, "after.join_request", domain.NotificationCrews, "Nuevo pedido para tu after",
			fmt.Sprintf("%s quiere entrar a %s.", name, a.name), "after.join_request:"+afterID+":"+userID,
			afterPushData(afterID, map[string]string{"user_id": userID})); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit join after: %w", err)
	}
	return status, nil
}

func (r *AfterRepository) Leave(ctx context.Context, afterID, userID string) error {
	var host string
	err := r.db.QueryRowContext(ctx, `SELECT host_user_id FROM afters WHERE id = $1::uuid AND status <> 'removed'`, afterID).Scan(&host)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrAfterNotFound
	}
	if err != nil {
		return fmt.Errorf("reading after: %w", err)
	}
	if host == userID {
		return domain.ErrHostCannotLeaveAfter
	}
	// Idempotente. Un rechazo no se puede "cancelar".
	if _, err := r.db.ExecContext(ctx, `UPDATE after_attendees SET status = 'cancelled', updated_at = NOW()
 WHERE after_id = $1::uuid AND user_id = $2::uuid AND status IN ('requested', 'accepted', 'going')`, afterID, userID); err != nil {
		return fmt.Errorf("leaving after: %w", err)
	}
	return nil
}

func (r *AfterRepository) requireHost(ctx context.Context, afterID, hostID string) error {
	var host, status string
	err := r.db.QueryRowContext(ctx, `SELECT host_user_id, status FROM afters WHERE id = $1::uuid`, afterID).Scan(&host, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status == domain.AfterStatusRemoved) {
		return domain.ErrAfterNotFound
	}
	if err != nil {
		return fmt.Errorf("reading after: %w", err)
	}
	if host != hostID {
		return domain.ErrNotAfterHost
	}
	return nil
}

func (r *AfterRepository) ListRequests(ctx context.Context, afterID, hostID, status string) ([]domain.AfterRequest, error) {
	if err := r.requireHost(ctx, afterID, hostID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT u.id, COALESCE(u.name, ''), COALESCE(u.username, ''), NULLIF(u.avatar_url, ''),
 t.message, t.created_at, t.status
 FROM after_attendees t JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL AND u.suspended_at IS NULL
 WHERE t.after_id = $1::uuid AND t.status = $2 ORDER BY t.created_at DESC, u.id LIMIT 500`, afterID, status)
	if err != nil {
		return nil, fmt.Errorf("listing after requests: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AfterRequest, 0)
	for rows.Next() {
		var item domain.AfterRequest
		var avatar, message sql.NullString
		if err := rows.Scan(&item.UserID, &item.Name, &item.Username, &avatar, &message, &item.CreatedAt, &item.Status); err != nil {
			return nil, fmt.Errorf("scanning after request: %w", err)
		}
		if avatar.Valid {
			item.AvatarURL = &avatar.String
		}
		if message.Valid {
			item.Message = &message.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *AfterRepository) Decide(ctx context.Context, afterID, hostID, userID string, accept bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin decide after request: %w", err)
	}
	defer tx.Rollback()
	a, err := lockAfter(ctx, tx, afterID)
	if err != nil {
		return err
	}
	if a.status == domain.AfterStatusRemoved {
		return domain.ErrAfterNotFound
	}
	if a.hostID != hostID {
		return domain.ErrNotAfterHost
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM after_attendees WHERE after_id = $1::uuid AND user_id = $2::uuid FOR UPDATE`, afterID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrAfterRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("reading after request: %w", err)
	}
	if status != domain.AttendeeRequested {
		return domain.ErrAfterRequestDecided
	}
	next, kind, title, body := domain.AttendeeRejected, "after.join_request.rejected", "Tu pedido no fue aceptado",
		fmt.Sprintf("No pudieron sumarte a %s esta vez.", a.name)
	if accept {
		if a.ended || a.archived {
			return domain.ErrAfterEnded
		}
		if a.full() {
			return domain.ErrAfterFull
		}
		next, kind, title, body = domain.AttendeeAccepted, "after.join_request.accepted", "¡Te aceptaron en el after!",
			fmt.Sprintf("Ya podés ver los detalles de %s.", a.name)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE after_attendees SET status = $3, updated_at = NOW() WHERE after_id = $1::uuid AND user_id = $2::uuid`,
		afterID, userID, next); err != nil {
		return fmt.Errorf("deciding after request: %w", err)
	}
	if err := afterNotify(ctx, tx, userID, kind, domain.NotificationCrews, title, body, kind+":"+afterID+":"+userID,
		afterPushData(afterID, nil)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit decide after request: %w", err)
	}
	return nil
}

func (r *AfterRepository) Report(ctx context.Context, afterID, userID, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin report after: %w", err)
	}
	defer tx.Rollback()
	a, err := lockAfter(ctx, tx, afterID)
	if err != nil {
		return err
	}
	if a.status == domain.AfterStatusRemoved {
		return domain.ErrAfterNotFound
	}
	if a.hostID == userID {
		return domain.ErrCannotReportOwnAfter
	}
	// Una denuncia por usuario: repetirla no suma (es idempotente).
	if _, err := tx.ExecContext(ctx, `INSERT INTO after_reports (after_id, user_id, reason) VALUES ($1::uuid, $2::uuid, $3)
 ON CONFLICT (after_id, user_id) DO NOTHING`, afterID, userID, reason); err != nil {
		return fmt.Errorf("inserting after report: %w", err)
	}
	if a.status == domain.AfterStatusActive {
		// Solo cuentan las denuncias posteriores a la última revisión de un ADMIN (restore).
		if _, err := tx.ExecContext(ctx, `UPDATE afters a SET status = 'hidden', updated_at = NOW()
 WHERE a.id = $1::uuid AND a.status = 'active' AND (SELECT COUNT(*) FROM after_reports r
   WHERE r.after_id = a.id AND r.created_at > COALESCE(a.reviewed_at, '-infinity'::timestamptz)) >= $2`,
			afterID, domain.AfterReportsToHide); err != nil {
			return fmt.Errorf("hiding reported after: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit report after: %w", err)
	}
	return nil
}

func (r *AfterRepository) AdminList(ctx context.Context, status string, limit, offset int) ([]domain.AdminAfter, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+afterSummaryJSON("$1::uuid")+`, a.status,
 (SELECT COUNT(*) FROM after_reports r WHERE r.after_id = a.id),
 COALESCE((SELECT jsonb_agg(x.reason ORDER BY x.created_at DESC) FROM (SELECT reason, created_at FROM after_reports r
   WHERE r.after_id = a.id ORDER BY created_at DESC LIMIT 5) x), '[]'::jsonb)
 FROM afters a JOIN users hu ON hu.id = a.host_user_id LEFT JOIN events ev ON ev.id = a.event_id
 LEFT JOIN after_attendees va ON va.after_id = a.id AND va.user_id = $1::uuid
 WHERE a.status = $2 ORDER BY a.updated_at DESC, a.id LIMIT $3 OFFSET $4`, nil, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing afters for review: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AdminAfter, 0)
	for rows.Next() {
		var raw, reasons []byte
		var item domain.AdminAfter
		if err := rows.Scan(&raw, &item.Status, &item.ReportsCount, &reasons); err != nil {
			return nil, fmt.Errorf("scanning after for review: %w", err)
		}
		if item.AfterSummary, err = decodeAfterSummary(raw); err != nil {
			return nil, err
		}
		item.Reasons = []string{}
		if err := json.Unmarshal(reasons, &item.Reasons); err != nil {
			return nil, fmt.Errorf("decoding report reasons: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *AfterRepository) AdminSetStatus(ctx context.Context, afterID, adminID, status string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE afters SET status = $2, reviewed_at = NOW(), reviewed_by = $3::uuid, updated_at = NOW()
 WHERE id = $1::uuid`, afterID, status, adminID)
	if err != nil {
		return fmt.Errorf("updating after status: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return domain.ErrAfterNotFound
	}
	return nil
}

// decodeAftersEmbed lee la columna `afters` del detalle del evento. Si viniera mal, el evento se devuelve igual sin
// el embed (se loguea sin datos).
func decodeAftersEmbed(raw []byte) *domain.AftersEventSummary {
	if len(raw) == 0 {
		return nil
	}
	var embed domain.AftersEventSummary
	if err := json.Unmarshal(raw, &embed); err != nil {
		log.Printf("event detail: decoding afters embed: %v", err)
		return nil
	}
	if embed.Preview == nil {
		embed.Preview = []domain.AfterSummary{}
	}
	for i := range embed.Preview {
		if embed.Preview[i].Genres == nil {
			embed.Preview[i].Genres = []string{}
		}
	}
	if embed.ViewerAfterIDs == nil {
		embed.ViewerAfterIDs = []string{}
	}
	return &embed
}
