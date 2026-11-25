package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// Crews temporales sobre las tablas existentes: `squads` (type='temporary'), `squad_members`, `squad_events`
// y el chat CREWS. Solo se agregan columnas y la tabla `crew_join_requests`.

const (
	tempCrewEventUniqueIndex = "squad_members_temp_event_user_uidx"
	// Hay una sola instancia del job de cierre a la vez, aunque corran varias réplicas del API.
	tempCrewCloseLockKey = 7340211
	joinRequestsPerHour  = 20
)

// El evento terminó al llegar endDate o, si no hay, 12 h después de su inicio. Desde ahí la crew sigue en la app
// (chat, integrantes y pasos) pero ya no admite a nadie.
const eventEndedExpr = `(COALESCE(e.end_date, e.date::timestamptz + INTERVAL '12 hours') < NOW())`

type TempCrewRepository struct{ db *sql.DB }

var _ ports.TempCrewRepository = (*TempCrewRepository)(nil)

func NewTempCrewRepository(db *sql.DB) *TempCrewRepository { return &TempCrewRepository{db: db} }

func (r *TempCrewRepository) InitSchema(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning temporary crew schema migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		// `squads.type` ya existe ('permanent', 'event_match'): 'temporary' es un valor más.
		`ALTER TABLE squads ADD COLUMN IF NOT EXISTS capacity SMALLINT`,
		`ALTER TABLE squads ADD COLUMN IF NOT EXISTS plan TEXT`,
		`ALTER TABLE squads ADD COLUMN IF NOT EXISTS is_admitting BOOLEAN NOT NULL DEFAULT TRUE`,
		`DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'squads_temporary_fields' AND conrelid = 'squads'::regclass) THEN
   ALTER TABLE squads ADD CONSTRAINT squads_temporary_fields CHECK (
    type IS DISTINCT FROM 'temporary' OR (event_id IS NOT NULL AND capacity BETWEEN 2 AND 10
     AND plan IS NOT NULL AND char_length(plan) BETWEEN 1 AND 280));
  END IF;
 END $$`,
		`CREATE INDEX IF NOT EXISTS squads_temporary_event_idx ON squads (event_id) WHERE type = 'temporary'`,
		// Una crew temporal por persona y evento, también bajo concurrencia.
		`ALTER TABLE squad_members ADD COLUMN IF NOT EXISTS temp_event_id UUID`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ` + tempCrewEventUniqueIndex + ` ON squad_members (temp_event_id, user_id) WHERE temp_event_id IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS crew_join_requests (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 crew_id UUID NOT NULL REFERENCES squads(id) ON DELETE CASCADE,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 status TEXT NOT NULL CHECK (status IN ('pending','accepted','rejected','cancelled')),
 message TEXT NULL CHECK (char_length(message) <= 280),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 decided_at TIMESTAMPTZ NULL,
 decided_by UUID NULL REFERENCES users(id) ON DELETE SET NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS crew_join_requests_one_pending ON crew_join_requests (crew_id, user_id) WHERE status = 'pending'`,
		`CREATE INDEX IF NOT EXISTS crew_join_requests_crew_status ON crew_join_requests (crew_id, status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS crew_join_requests_user_pending ON crew_join_requests (user_id) WHERE status = 'pending'`,
		`CREATE INDEX IF NOT EXISTS crew_join_requests_user_created ON crew_join_requests (user_id, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("temporary crew migration: %w", err)
		}
	}
	return tx.Commit()
}

func nullUUID(value string) interface{} {
	if _, err := uuid.Parse(value); err != nil {
		return nil
	}
	return value
}

func isUniqueViolation(err error, constraint string) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505" && (constraint == "" || pqErr.Constraint == constraint)
}

// ---------------------------------------------------------------- lecturas

// $1 = quien consulta (uuid o NULL). Las condiciones extra se anexan con AND.
const tempCrewSelect = `SELECT s.id, s.name, COALESCE(s.plan, ''), COALESCE(s.capacity, 0), mc.n,
 (COALESCE(s.is_admitting, TRUE) AND NOT ` + eventEndedExpr + ` AND mc.n < COALESCE(s.capacity, 0)),
 hu.id, hu.name, hu.username, hu.avatar_url,
 e.id, e.title, e.date, e.location, e.cinematic_banner_url,
 vm.role, vr.status,
 (SELECT COUNT(*) FROM crew_join_requests q WHERE q.crew_id = s.id AND q.status = 'pending'),
 ` + eventEndedExpr + `,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('userId', x.user_id, 'name', x.name, 'username', x.username, 'avatarUrl', x.avatar_url) ORDER BY x.ord)
   FROM (SELECT sm.user_id, u.name, u.username, u.avatar_url,
          ROW_NUMBER() OVER (ORDER BY (sm.role = 'host') DESC, sm.joined_at, sm.user_id) AS ord
         FROM squad_members sm JOIN users u ON u.id = sm.user_id WHERE sm.squad_id = s.id ORDER BY ord LIMIT 4) x), '[]'::jsonb)
FROM squads s
JOIN events e ON e.id = s.event_id
CROSS JOIN LATERAL (SELECT COUNT(*)::int AS n FROM squad_members WHERE squad_id = s.id) mc
JOIN squad_members hm ON hm.squad_id = s.id AND hm.role = 'host'
JOIN users hu ON hu.id = hm.user_id
LEFT JOIN squad_members vm ON vm.squad_id = s.id AND vm.user_id = $1::uuid
LEFT JOIN LATERAL (SELECT status FROM crew_join_requests WHERE crew_id = s.id AND user_id = $1::uuid ORDER BY created_at DESC LIMIT 1) vr ON TRUE
WHERE s.type = 'temporary'`

type tempCrewRow struct {
	summary domain.TempCrewSummary
	pending int
}

// scanTempCrew lee una fila de tempCrewSelect. `lead` son destinos extra para columnas que se anteponen al SELECT
// (por ejemplo el total con COUNT(*) OVER()).
func scanTempCrew(scan func(dest ...interface{}) error, lead ...interface{}) (tempCrewRow, error) {
	var row tempCrewRow
	var hostAvatar, banner, role, requestStatus sql.NullString
	var preview []byte
	s := &row.summary
	dest := append(lead, &s.ID, &s.Name, &s.Plan, &s.Capacity, &s.MemberCount, &s.IsAdmitting,
		&s.Host.UserID, &s.Host.Name, &s.Host.Username, &hostAvatar,
		&s.Event.ID, &s.Event.Title, &s.Event.Date, &s.Event.Location, &banner,
		&role, &requestStatus, &row.pending, &s.EventEnded, &preview)
	err := scan(dest...)
	if err != nil {
		return row, err
	}
	if hostAvatar.Valid {
		s.Host.AvatarURL = &hostAvatar.String
	}
	if banner.Valid {
		s.Event.CinematicBannerURL = &banner.String
	}
	if role.Valid {
		s.ViewerRole = &role.String
	} else if requestStatus.Valid {
		// Quien ya es integrante no tiene estado de solicitud.
		s.ViewerRequestStatus = &requestStatus.String
	}
	if s.ViewerRole != nil && *s.ViewerRole == "host" {
		count := row.pending
		s.PendingRequestsCount = &count
	}
	s.MembersPreview = []domain.TempCrewPerson{}
	if err := json.Unmarshal(preview, &s.MembersPreview); err != nil {
		return row, fmt.Errorf("decoding members preview: %w", err)
	}
	return row, nil
}

func (r *TempCrewRepository) queryCrews(ctx context.Context, q string, args ...interface{}) ([]tempCrewRow, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]tempCrewRow, 0)
	for rows.Next() {
		row, err := scanTempCrew(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func summaries(rows []tempCrewRow) []domain.TempCrewSummary {
	out := make([]domain.TempCrewSummary, len(rows))
	for i := range rows {
		out[i] = rows[i].summary
	}
	return out
}

// Mismas crews para GET /v1/events/{id}/temp-crews y para el resumen del detalle del evento: las que admiten gente,
// con más lugares libres primero y después las más nuevas. El total sale en la misma consulta (COUNT(*) OVER()).
const openCrewsFilter = ` AND s.event_id = $2::uuid AND COALESCE(s.is_admitting, TRUE) AND NOT ` + eventEndedExpr + ` AND mc.n < s.capacity`

var tempCrewSelectWithTotal = "SELECT COUNT(*) OVER(), " + strings.TrimPrefix(tempCrewSelect, "SELECT ")

// ListOpen devuelve la página de crews que admiten gente, el total que admite y la crew temporal de quien consulta
// en ese evento (aunque esté llena o cerrada). No valida que el evento exista.
func (r *TempCrewRepository) ListOpen(ctx context.Context, eventID, viewerID string, limit, offset int) ([]domain.TempCrewSummary, int, *string, error) {
	var (
		wg           sync.WaitGroup
		viewerCrewID *string
		viewerErr    error
	)
	// Se busca en paralelo: usa el índice (temp_event_id, user_id) y no depende de la página.
	if nullUUID(viewerID) != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var crewID string
			err := r.db.QueryRowContext(ctx, `SELECT squad_id FROM squad_members WHERE temp_event_id = $1::uuid AND user_id = $2::uuid`, eventID, viewerID).Scan(&crewID)
			switch {
			case err == nil:
				viewerCrewID = &crewID
			case !errors.Is(err, sql.ErrNoRows):
				viewerErr = fmt.Errorf("finding viewer crew: %w", err)
			}
		}()
	}

	total := 0
	rows, err := r.db.QueryContext(ctx, tempCrewSelectWithTotal+openCrewsFilter+`
 ORDER BY (s.capacity - mc.n) DESC, s.created_at DESC, s.id LIMIT $3 OFFSET $4`, nullUUID(viewerID), eventID, limit, offset)
	items := make([]domain.TempCrewSummary, 0)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			row, scanErr := scanTempCrew(rows.Scan, &total)
			if scanErr != nil {
				err = scanErr
				break
			}
			items = append(items, row.summary)
		}
		if err == nil {
			err = rows.Err()
		}
	}
	wg.Wait()
	if err != nil {
		return nil, 0, nil, fmt.Errorf("listing temporary crews: %w", err)
	}
	if viewerErr != nil {
		return nil, 0, nil, viewerErr
	}
	// Una página vacía más allá del final no trae el total: se cuenta aparte (solo ocurre con offset > 0).
	if len(items) == 0 && offset > 0 {
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM squads s JOIN events e ON e.id = s.event_id
 WHERE s.type = 'temporary' AND s.event_id = $1::uuid AND COALESCE(s.is_admitting, TRUE) AND NOT `+eventEndedExpr+`
 AND (SELECT COUNT(*) FROM squad_members m WHERE m.squad_id = s.id) < s.capacity`, eventID).Scan(&total); err != nil {
			return nil, 0, nil, fmt.Errorf("counting temporary crews: %w", err)
		}
	}
	return items, total, viewerCrewID, nil
}

func (r *TempCrewRepository) ListForEvent(ctx context.Context, eventID, viewerID string, limit, offset int) (domain.TempCrewListPage, error) {
	page := domain.TempCrewListPage{Data: []domain.TempCrewSummary{}}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1::uuid)`, eventID).Scan(&exists); err != nil {
		return page, fmt.Errorf("checking event: %w", err)
	}
	if !exists {
		return page, domain.ErrEventNotFound
	}
	items, total, viewerCrewID, err := r.ListOpen(ctx, eventID, viewerID, limit, offset)
	if err != nil {
		return page, err
	}
	page.Data = items
	page.Meta = domain.TempCrewListMeta{Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total, ViewerCrewID: viewerCrewID}
	return page, nil
}

// EventSummary es lo que el detalle del evento agrega bajo `tempCrews`: lo mismo que
// GET /v1/events/{id}/temp-crews?limit={limit}&offset=0. El evento ya fue validado por quien llama.
func (r *TempCrewRepository) EventSummary(ctx context.Context, eventID, viewerID string, limit int) (*domain.TempCrewsEventSummary, error) {
	items, total, viewerCrewID, err := r.ListOpen(ctx, eventID, viewerID, limit, 0)
	if err != nil {
		return nil, err
	}
	return &domain.TempCrewsEventSummary{Total: total, ViewerCrewID: viewerCrewID, Preview: items}, nil
}

func (r *TempCrewRepository) Summary(ctx context.Context, crewID, viewerID string) (*domain.TempCrewSummary, error) {
	rows, err := r.queryCrews(ctx, tempCrewSelect+` AND s.id = $2::uuid`, nullUUID(viewerID), crewID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.ErrTempCrewNotFound
	}
	return &rows[0].summary, nil
}

func (r *TempCrewRepository) Detail(ctx context.Context, crewID, viewerID string) (*domain.TempCrewDetail, error) {
	rows, err := r.queryCrews(ctx, tempCrewSelect+` AND s.id = $2::uuid`, nullUUID(viewerID), crewID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := rows[0].summary
	return &domain.TempCrewDetail{
		Event: s.Event, Plan: s.Plan, Capacity: s.Capacity, IsAdmitting: s.IsAdmitting, HostID: s.Host.UserID,
		ViewerRole: s.ViewerRole, ViewerRequestStatus: s.ViewerRequestStatus, PendingRequestsCount: s.PendingRequestsCount,
		EventEnded: s.EventEnded,
	}, nil
}

func (r *TempCrewRepository) ListMine(ctx context.Context, userID string) ([]domain.TempCrewSummary, error) {
	rows, err := r.queryCrews(ctx, tempCrewSelect+`
 AND (vm.user_id IS NOT NULL OR vr.status = 'pending')
 ORDER BY `+eventEndedExpr+`, e.date ASC, s.created_at, s.id`, nullUUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing my temporary crews: %w", err)
	}
	return summaries(rows), nil
}

func (r *TempCrewRepository) Discover(ctx context.Context, userID string, limit int) ([]domain.TempCrewSummary, error) {
	rows, err := r.queryCrews(ctx, tempCrewSelect+`
 AND COALESCE(s.is_admitting, TRUE) AND NOT `+eventEndedExpr+` AND mc.n < s.capacity
 AND e.date::timestamptz > NOW()
 AND EXISTS (SELECT 1 FROM event_rsvps rv WHERE rv.user_id = $1::uuid AND rv.event_id = s.event_id AND rv.status = 'going')
 AND vm.user_id IS NULL AND vr.status IS DISTINCT FROM 'pending'
 AND NOT EXISTS (SELECT 1 FROM squad_members x WHERE x.temp_event_id = s.event_id AND x.user_id = $1::uuid)
 ORDER BY e.date ASC, (s.capacity - mc.n) DESC, s.created_at DESC, s.id LIMIT $2`, nullUUID(userID), limit)
	if err != nil {
		return nil, fmt.Errorf("discovering temporary crews: %w", err)
	}
	return summaries(rows), nil
}

// ---------------------------------------------------------------- helpers de escritura

func displayName(ctx context.Context, tx *sql.Tx, userID string) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(TRIM(name), ''), username) FROM users WHERE id = $1::uuid`, userID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrInvalidID
	}
	return name, err
}

// Marcar "Voy" es lo mismo que hace POST /v1/events/:id/rsvp. Decisión: un "No voy" previo se pisa, porque
// pedir o armar una crew para el evento es una declaración de que se va.
func markGoing(ctx context.Context, tx *sql.Tx, userID, eventID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO event_rsvps (user_id, event_id, status, created_at)
 VALUES ($1::uuid, $2::uuid, 'going', NOW())
 ON CONFLICT (user_id, event_id) DO UPDATE SET status = 'going', created_at = NOW()
 WHERE event_rsvps.status IS DISTINCT FROM 'going'`, userID, eventID)
	return err
}

// Aviso centrado en el chat. Va firmado por quien actúa; el trigger de push solo encola text/image.
func systemMessage(ctx context.Context, tx *sql.Tx, chatID, senderID, content string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages (id, chat_id, sender_id, content, type, timestamp)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'system', $5)`, uuid.NewString(), chatID, senderID, content, now); err != nil {
		return fmt.Errorf("inserting system message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET last_message = $2, updated_at = $3 WHERE id = $1::uuid`, chatID, content, now); err != nil {
		return fmt.Errorf("updating chat preview: %w", err)
	}
	return nil
}

// El push sale del trigger sobre `notifications`. Nunca lleva el texto de la solicitud.
func notifyTx(ctx context.Context, tx *sql.Tx, userID, kind, title, body, dedupe string, data map[string]string) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notifications (id, user_id, type, category, title, body, data, dedupe_key, scheduled_at)
 VALUES ($1, $2::uuid, $3, 'crews', $4, $5, $6, $7, NOW()) ON CONFLICT DO NOTHING`,
		uuid.NewString(), userID, kind, title, body, payload, dedupe)
	return err
}

func chatCreated(ctx context.Context, tx *sql.Tx, chatID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_notify('root_chat_events', jsonb_build_object('type', 'chat.created', 'chat_id', $1::text)::text)`, chatID)
	return err
}

const joinRequestSelect = `SELECT r.id, r.crew_id, r.status, r.message, r.created_at,
 u.id, u.name, u.username, u.avatar_url, COALESCE(u.is_kyc_verified, FALSE),
 EXISTS (SELECT 1 FROM event_rsvps ev WHERE ev.user_id = r.user_id AND ev.event_id = s.event_id AND ev.status = 'going'),
 COALESCE((SELECT COUNT(*) FROM (
   SELECT unnest(COALESCE(hu.following, '{}'::text[])) INTERSECT SELECT unnest(COALESCE(u.followers, '{}'::text[]))) m), 0)
FROM crew_join_requests r
JOIN squads s ON s.id = r.crew_id
JOIN users u ON u.id = r.user_id
LEFT JOIN squad_members hm ON hm.squad_id = s.id AND hm.role = 'host'
LEFT JOIN users hu ON hu.id = hm.user_id`

type rowQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func queryJoinRequests(ctx context.Context, q rowQueryer, where string, args ...interface{}) ([]domain.TempCrewJoinRequest, error) {
	rows, err := q.QueryContext(ctx, joinRequestSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TempCrewJoinRequest, 0)
	for rows.Next() {
		var req domain.TempCrewJoinRequest
		var message, avatar sql.NullString
		if err := rows.Scan(&req.ID, &req.CrewID, &req.Status, &message, &req.CreatedAt,
			&req.User.UserID, &req.User.Name, &req.User.Username, &avatar, &req.User.IsKycVerified,
			&req.IsGoingToEvent, &req.MutualFriendsCount); err != nil {
			return nil, err
		}
		if message.Valid {
			req.Message = &message.String
		}
		if avatar.Valid {
			req.User.AvatarURL = &avatar.String
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func loadJoinRequest(ctx context.Context, q rowQueryer, requestID string) (domain.TempCrewJoinRequest, error) {
	list, err := queryJoinRequests(ctx, q, ` WHERE r.id = $1::uuid`, requestID)
	if err != nil {
		return domain.TempCrewJoinRequest{}, err
	}
	if len(list) == 0 {
		return domain.TempCrewJoinRequest{}, domain.ErrRequestNotFound
	}
	return list[0], nil
}

// ---------------------------------------------------------------- escrituras

func (r *TempCrewRepository) Create(ctx context.Context, hostID, eventID string, input domain.CreateTempCrewInput) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin create temporary crew: %w", err)
	}
	defer tx.Rollback()

	var title string
	var ended bool
	err = tx.QueryRowContext(ctx, `SELECT e.title, `+eventEndedExpr+` FROM events e WHERE e.id = $1::uuid`, eventID).Scan(&title, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrEventNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading event: %w", err)
	}
	if ended {
		return "", domain.ErrEventEnded
	}
	hostName, err := displayName(ctx, tx, hostID)
	if err != nil {
		return "", err
	}

	crewID, chatID, now := uuid.NewString(), uuid.NewString(), time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO squads (id, event_id, name, chat_room_id, status, created_at, type, is_public, capacity, plan, is_admitting)
 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, 'active', $5, 'temporary', FALSE, $6, $7, TRUE)`,
		crewID, eventID, input.Name, chatID, now, input.Capacity, input.Plan); err != nil {
		return "", fmt.Errorf("inserting temporary crew: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at, temp_event_id)
 VALUES ($1::uuid, $2::uuid, 'host', FALSE, $3, $4::uuid)`, crewID, hostID, now, eventID); err != nil {
		if isUniqueViolation(err, tempCrewEventUniqueIndex) {
			return "", domain.ErrAlreadyInEventCrew
		}
		return "", fmt.Errorf("inserting temporary crew host: %w", err)
	}
	// El vínculo crew↔evento que usan rankings y pasos.
	if _, err := tx.ExecContext(ctx, `INSERT INTO squad_events (squad_id, event_id, created_at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`, crewID, eventID, now); err != nil {
		return "", fmt.Errorf("linking temporary crew event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chats (id, type, last_message, created_at, updated_at) VALUES ($1::uuid, 'CREWS', '', $2, $2)`, chatID, now); err != nil {
		return "", fmt.Errorf("creating temporary crew chat: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_participants (chat_id, user_id, joined_at) VALUES ($1::uuid, $2::uuid, $3)`, chatID, hostID, now); err != nil {
		return "", fmt.Errorf("adding host to chat: %w", err)
	}
	if err := systemMessage(ctx, tx, chatID, hostID, fmt.Sprintf("%s creó la crew para %s", hostName, title), now); err != nil {
		return "", err
	}
	if err := markGoing(ctx, tx, hostID, eventID); err != nil {
		return "", fmt.Errorf("marking host as going: %w", err)
	}
	if err := chatCreated(ctx, tx, chatID); err != nil {
		return "", fmt.Errorf("publishing chat.created: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit create temporary crew: %w", err)
	}
	return crewID, nil
}

func (r *TempCrewRepository) RequestJoin(ctx context.Context, crewID, userID string, message *string) (domain.TempCrewJoinRequest, bool, error) {
	var none domain.TempCrewJoinRequest
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return none, false, fmt.Errorf("begin join request: %w", err)
	}
	defer tx.Rollback()

	var eventID, crewName, hostID string
	var capacity, members int
	var admitting, ended bool
	err = tx.QueryRowContext(ctx, `SELECT s.event_id, s.name, COALESCE(s.capacity, 0), COALESCE(s.is_admitting, TRUE), `+eventEndedExpr+`,
 (SELECT COUNT(*) FROM squad_members WHERE squad_id = s.id), hm.user_id
 FROM squads s JOIN events e ON e.id = s.event_id JOIN squad_members hm ON hm.squad_id = s.id AND hm.role = 'host'
 WHERE s.id = $1::uuid AND s.type = 'temporary'`, crewID).Scan(&eventID, &crewName, &capacity, &admitting, &ended, &members, &hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, domain.ErrTempCrewNotFound
	}
	if err != nil {
		return none, false, fmt.Errorf("reading temporary crew: %w", err)
	}

	var isMember bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squad_members WHERE squad_id = $1::uuid AND user_id = $2::uuid)`, crewID, userID).Scan(&isMember); err != nil {
		return none, false, err
	}
	if isMember {
		return none, false, domain.ErrAlreadyMember
	}
	// Ya hay una solicitud pendiente: es idempotente, devuelve la existente.
	var pendingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM crew_join_requests WHERE crew_id = $1::uuid AND user_id = $2::uuid AND status = 'pending'`, crewID, userID).Scan(&pendingID)
	if err == nil {
		existing, err := loadJoinRequest(ctx, tx, pendingID)
		return existing, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, false, err
	}
	var inEventCrew bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM squad_members WHERE temp_event_id = $1::uuid AND user_id = $2::uuid)`, eventID, userID).Scan(&inEventCrew); err != nil {
		return none, false, err
	}
	if inEventCrew {
		return none, false, domain.ErrAlreadyInEventCrew
	}
	if ended {
		return none, false, domain.ErrEventEnded
	}
	if !admitting || members >= capacity {
		return none, false, domain.ErrNotAdmitting
	}
	var recent int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM crew_join_requests WHERE user_id = $1::uuid AND created_at > NOW() - INTERVAL '1 hour'`, userID).Scan(&recent); err != nil {
		return none, false, err
	}
	if recent >= joinRequestsPerHour {
		return none, false, domain.ErrTooManyRequests
	}

	requesterName, err := displayName(ctx, tx, userID)
	if err != nil {
		return none, false, err
	}
	requestID := uuid.NewString()
	var msg interface{}
	if message != nil {
		msg = *message
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO crew_join_requests (id, crew_id, user_id, status, message) VALUES ($1::uuid, $2::uuid, $3::uuid, 'pending', $4)`,
		requestID, crewID, userID, msg); err != nil {
		if isUniqueViolation(err, "crew_join_requests_one_pending") {
			// Otra petición idéntica ganó la carrera: devolvé esa.
			_ = tx.Rollback()
			var existing string
			if qerr := r.db.QueryRowContext(ctx, `SELECT id FROM crew_join_requests WHERE crew_id = $1::uuid AND user_id = $2::uuid AND status = 'pending'`, crewID, userID).Scan(&existing); qerr != nil {
				return none, false, qerr
			}
			req, qerr := loadJoinRequest(ctx, r.db, existing)
			return req, false, qerr
		}
		return none, false, fmt.Errorf("inserting join request: %w", err)
	}
	if err := markGoing(ctx, tx, userID, eventID); err != nil {
		return none, false, fmt.Errorf("marking requester as going: %w", err)
	}
	if err := notifyTx(ctx, tx, hostID, "crew.join_request", "Nueva solicitud para tu crew",
		fmt.Sprintf("%s quiere unirse a %s.", requesterName, crewName), "crew.join_request:"+requestID,
		map[string]string{"crew_id": crewID, "request_id": requestID}); err != nil {
		return none, false, fmt.Errorf("notifying host: %w", err)
	}
	req, err := loadJoinRequest(ctx, tx, requestID)
	if err != nil {
		return none, false, err
	}
	if err := tx.Commit(); err != nil {
		return none, false, fmt.Errorf("commit join request: %w", err)
	}
	return req, true, nil
}

func (r *TempCrewRepository) CancelMyRequest(ctx context.Context, crewID, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE crew_join_requests SET status = 'cancelled', decided_at = NOW()
 WHERE crew_id = $1::uuid AND user_id = $2::uuid AND status = 'pending'`, crewID, userID)
	return err
}

// requireHost valida que la crew sea temporal y que hostID sea su host. Devuelve los datos básicos.
func requireHost(ctx context.Context, tx *sql.Tx, crewID, hostID string, lock bool) (eventID, chatID, name string, capacity int, ended bool, err error) {
	q := `SELECT s.event_id, COALESCE(s.chat_room_id::text, ''), s.name, COALESCE(s.capacity, 0), ` + eventEndedExpr + `,
 EXISTS(SELECT 1 FROM squad_members WHERE squad_id = s.id AND user_id = $2::uuid AND role = 'host')
 FROM squads s JOIN events e ON e.id = s.event_id WHERE s.id = $1::uuid AND s.type = 'temporary'`
	if lock {
		q += ` FOR UPDATE OF s`
	}
	var isHost bool
	err = tx.QueryRowContext(ctx, q, crewID, hostID).Scan(&eventID, &chatID, &name, &capacity, &ended, &isHost)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", 0, false, domain.ErrTempCrewNotFound
	}
	if err != nil {
		return "", "", "", 0, false, err
	}
	if !isHost {
		return "", "", "", 0, false, domain.ErrNotTempCrewHost
	}
	return
}

func (r *TempCrewRepository) ListRequests(ctx context.Context, crewID, hostID, status string, limit, offset int) ([]domain.TempCrewJoinRequest, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, _, _, _, err := requireHost(ctx, tx, crewID, hostID, false); err != nil {
		return nil, err
	}
	return queryJoinRequests(ctx, tx, ` WHERE r.crew_id = $1::uuid AND r.status = $2 ORDER BY r.created_at DESC, r.id LIMIT $3 OFFSET $4`, crewID, status, limit, offset)
}

func (r *TempCrewRepository) Decide(ctx context.Context, crewID, requestID, hostID string, accept bool) (domain.TempCrewJoinRequest, error) {
	var none domain.TempCrewJoinRequest
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return none, fmt.Errorf("begin decide join request: %w", err)
	}
	defer tx.Rollback()

	// El bloqueo de la fila de la crew serializa las aceptaciones: dos hosts-tab o dos reintentos no pasan el cupo.
	eventID, chatID, crewName, capacity, ended, err := requireHost(ctx, tx, crewID, hostID, true)
	if err != nil {
		return none, err
	}
	var requesterID, status string
	err = tx.QueryRowContext(ctx, `SELECT user_id, status FROM crew_join_requests WHERE id = $1::uuid AND crew_id = $2::uuid FOR UPDATE`, requestID, crewID).Scan(&requesterID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return none, domain.ErrRequestNotFound
	}
	if err != nil {
		return none, err
	}
	if status != domain.JoinRequestPending {
		return none, domain.ErrRequestNotPending
	}
	now := time.Now().UTC()

	if !accept {
		if _, err := tx.ExecContext(ctx, `UPDATE crew_join_requests SET status = 'rejected', decided_at = $2, decided_by = $3::uuid WHERE id = $1::uuid`, requestID, now, hostID); err != nil {
			return none, err
		}
		if err := notifyTx(ctx, tx, requesterID, "crew.join_request.rejected", "Tu solicitud no fue aceptada",
			fmt.Sprintf("%s no pudo sumarte esta vez.", crewName), "crew.join_request.rejected:"+requestID,
			map[string]string{"crew_id": crewID}); err != nil {
			return none, err
		}
		req, err := loadJoinRequest(ctx, tx, requestID)
		if err != nil {
			return none, err
		}
		return req, tx.Commit()
	}

	// Terminó el evento: se puede rechazar, pero ya no sumar a nadie.
	if ended {
		return none, domain.ErrEventEnded
	}
	var members int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM squad_members WHERE squad_id = $1::uuid`, crewID).Scan(&members); err != nil {
		return none, err
	}
	if members >= capacity {
		return none, domain.ErrCrewFull
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO squad_members (squad_id, user_id, role, has_ticket, joined_at, temp_event_id)
 VALUES ($1::uuid, $2::uuid, 'member', FALSE, $3, $4::uuid)`, crewID, requesterID, now, eventID); err != nil {
		if isUniqueViolation(err, tempCrewEventUniqueIndex) {
			return none, domain.ErrAlreadyInEventCrew
		}
		return none, fmt.Errorf("inserting member: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE crew_join_requests SET status = 'accepted', decided_at = $2, decided_by = $3::uuid WHERE id = $1::uuid`, requestID, now, hostID); err != nil {
		return none, err
	}
	// Una sola crew temporal por evento: las demás solicitudes pendientes de esta persona se cancelan.
	if _, err := tx.ExecContext(ctx, `UPDATE crew_join_requests SET status = 'cancelled', decided_at = $3
 WHERE user_id = $1::uuid AND status = 'pending' AND id <> $2::uuid
 AND crew_id IN (SELECT id FROM squads WHERE type = 'temporary' AND event_id = $4::uuid)`, requesterID, requestID, now, eventID); err != nil {
		return none, err
	}
	if chatID == "" {
		return none, errors.New("temporary crew without chat")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_participants (chat_id, user_id, joined_at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`, chatID, requesterID, now); err != nil {
		return none, fmt.Errorf("adding member to chat: %w", err)
	}
	hostName, err := displayName(ctx, tx, hostID)
	if err != nil {
		return none, err
	}
	requesterName, err := displayName(ctx, tx, requesterID)
	if err != nil {
		return none, err
	}
	if err := systemMessage(ctx, tx, chatID, hostID, fmt.Sprintf("%s aceptó a %s", hostName, requesterName), now); err != nil {
		return none, err
	}
	if err := notifyTx(ctx, tx, requesterID, "crew.join_request.accepted", "¡Te aceptaron en la crew!",
		fmt.Sprintf("Ya sos parte de %s.", crewName), "crew.join_request.accepted:"+requestID,
		map[string]string{"crew_id": crewID, "chat_id": chatID}); err != nil {
		return none, err
	}
	// La lista de chats de la persona aceptada se entera de que apareció uno nuevo.
	if err := chatCreated(ctx, tx, chatID); err != nil {
		return none, err
	}
	req, err := loadJoinRequest(ctx, tx, requestID)
	if err != nil {
		return none, err
	}
	if err := tx.Commit(); err != nil {
		return none, fmt.Errorf("commit accept join request: %w", err)
	}
	return req, nil
}

func (r *TempCrewRepository) SetAdmitting(ctx context.Context, crewID, hostID string, admitting bool) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, _, _, _, _, err := requireHost(ctx, tx, crewID, hostID, true); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE squads SET is_admitting = $2 WHERE id = $1::uuid`, crewID, admitting); err != nil {
		return false, err
	}
	return admitting, tx.Commit()
}

// CloseEnded cancela las solicitudes pendientes de las crews cuyo evento terminó: ya nadie puede entrar y no tiene
// sentido que queden esperando. Las crews NO se ocultan ni se archivan: siguen en la app con su chat, sus
// integrantes y sus pasos. Es idempotente y seguro con varias réplicas (lock de PostgreSQL: corre una sola).
func (r *TempCrewRepository) CloseEnded(ctx context.Context) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, tempCrewCloseLockKey).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE crew_join_requests r SET status = 'cancelled', decided_at = NOW()
 FROM squads s JOIN events e ON e.id = s.event_id
 WHERE s.id = r.crew_id AND s.type = 'temporary' AND r.status = 'pending' AND `+eventEndedExpr)
	if err != nil {
		return 0, fmt.Errorf("cancelling requests of ended crews: %w", err)
	}
	cancelled, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return cancelled, tx.Commit()
}
