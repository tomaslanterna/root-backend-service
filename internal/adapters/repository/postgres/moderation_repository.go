package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"root-backend-service/internal/core/domain"
)

// ModerationRepository guarda bloqueos, denuncias y bajas de cuenta. Implementa ports.BlockRepository,
// ports.ReportRepository y ports.AccountRepository. Usa las tablas user_blocks, content_reports y deleted_accounts
// y las columnas users.deleted_at/suspended_at/accepted_terms_at/terms_version y posts.hidden_at, que ya existen en la base.
type ModerationRepository struct{ db *sql.DB }

func NewModerationRepository(db *sql.DB) *ModerationRepository {
	return &ModerationRepository{db: db}
}

// removedContentText reemplaza el texto de un comentario o mensaje que moderación eliminó.
const removedContentText = "Contenido eliminado por moderación"

// maxReportSnapshot acota la copia del contenido denunciado que se guarda con la denuncia.
const maxReportSnapshot = 2000

// ---------- Bloqueos ----------

func (r *ModerationRepository) Block(ctx context.Context, blockerID, blockedID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		// Se dejan de seguir en ambos sentidos.
		`UPDATE users SET followers = array_remove(COALESCE(followers, '{}'::text[]), $2::text),
			following = array_remove(COALESCE(following, '{}'::text[]), $2::text) WHERE id = $1`,
		`UPDATE users SET followers = array_remove(COALESCE(followers, '{}'::text[]), $1::text),
			following = array_remove(COALESCE(following, '{}'::text[]), $1::text) WHERE id = $2`,
		`DELETE FROM follow_requests WHERE (requester_id = $1 AND target_id = $2) OR (requester_id = $2 AND target_id = $1)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, blockerID, blockedID); err != nil {
			return fmt.Errorf("blocking user: %w", err)
		}
	}
	return tx.Commit()
}

func (r *ModerationRepository) Unblock(ctx context.Context, blockerID, blockedID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`, blockerID, blockedID)
	if err != nil {
		return false, fmt.Errorf("unblocking user: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (r *ModerationRepository) ListBlocked(ctx context.Context, blockerID string) ([]domain.BlockedUser, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT u.id, COALESCE(u.name, ''), u.username, u.avatar_url, b.created_at
		FROM user_blocks b JOIN users u ON u.id = b.blocked_id
		WHERE b.blocker_id = $1 AND u.deleted_at IS NULL
		ORDER BY b.created_at DESC`, blockerID)
	if err != nil {
		return nil, fmt.Errorf("listing blocked users: %w", err)
	}
	defer rows.Close()
	users := make([]domain.BlockedUser, 0)
	for rows.Next() {
		var u domain.BlockedUser
		var avatar sql.NullString
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &avatar, &u.BlockedAt); err != nil {
			return nil, fmt.Errorf("scanning blocked user: %w", err)
		}
		if avatar.Valid {
			u.AvatarURL = &avatar.String
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *ModerationRepository) BlockState(ctx context.Context, viewerID, otherID string) (bool, bool, error) {
	var byViewer, byOther bool
	err := r.db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2),
		EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $2 AND blocked_id = $1)`, viewerID, otherID).Scan(&byViewer, &byOther)
	if err != nil {
		return false, false, fmt.Errorf("reading block state: %w", err)
	}
	return byViewer, byOther, nil
}

// ---------- Denuncias ----------

// reportTargetQueries devuelven (autor, texto) del contenido denunciado. $1 es el id del contenido y $2 quien
// denuncia: un mensaje solo lo puede denunciar alguien que participa del chat.
var reportTargetQueries = map[domain.ReportTargetType]string{
	domain.ReportTargetPost: `SELECT p.author_id::text,
		trim(concat_ws(' ', NULLIF(trim(p.title), ''), NULLIF(trim(p.content), ''), CASE WHEN p.header_image_url IS NOT NULL THEN '[imagen: ' || p.header_image_url || ']' END))
		FROM posts p WHERE p.id = $1 AND p.hidden_at IS NULL AND $2::text IS NOT NULL`,
	domain.ReportTargetComment: `SELECT c.author_id::text, COALESCE(c.content, '') FROM comments c WHERE c.id = $1 AND $2::text IS NOT NULL`,
	domain.ReportTargetMessage: `SELECT m.sender_id::text,
		CASE WHEN m.type = 'image' THEN '[imagen: ' || COALESCE(m.content, '') || ']' ELSE COALESCE(m.content, '') END
		FROM messages m JOIN chat_participants cp ON cp.chat_id = m.chat_id AND cp.user_id = $2
		WHERE m.id = $1`,
	domain.ReportTargetUser: `SELECT u.id::text, COALESCE(u.name, '') || ' (@' || u.username || ')'
		FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL AND $2::text IS NOT NULL`,
}

func (r *ModerationRepository) CreateReport(ctx context.Context, reporterID string, input domain.ReportInput) (string, bool, error) {
	query, ok := reportTargetQueries[input.TargetType]
	if !ok {
		return "", false, domain.ErrInvalidReport
	}
	var targetUserID, snapshot sql.NullString
	err := r.db.QueryRowContext(ctx, query, input.TargetID, reporterID).Scan(&targetUserID, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, domain.ErrReportTargetNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("resolving report target: %w", err)
	}
	if targetUserID.Valid && targetUserID.String == reporterID {
		// No tiene sentido denunciar lo propio.
		return "", false, domain.ErrInvalidReport
	}
	text := []rune(snapshot.String)
	if len(text) > maxReportSnapshot {
		text = text[:maxReportSnapshot]
	}
	var id string
	err = r.db.QueryRowContext(ctx, `INSERT INTO content_reports
		(id, reporter_id, target_type, target_id, target_user_id, reason, details, content_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (reporter_id, target_type, target_id) DO NOTHING
		RETURNING id`,
		uuid.NewString(), reporterID, string(input.TargetType), input.TargetID, nullString(targetUserID), input.Reason, input.Details, string(text),
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("creating report: %w", err)
	}
	// Ya la había denunciado: se devuelve la denuncia existente.
	err = r.db.QueryRowContext(ctx, `SELECT id FROM content_reports WHERE reporter_id = $1 AND target_type = $2 AND target_id = $3`,
		reporterID, string(input.TargetType), input.TargetID).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("reading existing report: %w", err)
	}
	return id, false, nil
}

func nullString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

const reportSelect = `SELECT r.id, r.reporter_id, COALESCE(rep.username, ''), r.target_type, r.target_id, r.target_user_id,
	COALESCE(tgt.username, ''), r.reason, r.details, r.content_snapshot, r.status, r.created_at, r.reviewed_at
	FROM content_reports r
	LEFT JOIN users rep ON rep.id = r.reporter_id
	LEFT JOIN users tgt ON tgt.id = r.target_user_id`

func scanReport(row interface{ Scan(...any) error }) (domain.ContentReport, error) {
	var report domain.ContentReport
	var reporterID, targetUserID sql.NullString
	var targetType string
	var reviewedAt sql.NullTime
	err := row.Scan(&report.ID, &reporterID, &report.ReporterUsername, &targetType, &report.TargetID, &targetUserID,
		&report.TargetUsername, &report.Reason, &report.Details, &report.ContentSnapshot, &report.Status, &report.CreatedAt, &reviewedAt)
	if err != nil {
		return report, err
	}
	report.TargetType = domain.ReportTargetType(targetType)
	if reporterID.Valid {
		report.ReporterID = &reporterID.String
	}
	if targetUserID.Valid {
		report.TargetUserID = &targetUserID.String
	}
	if reviewedAt.Valid {
		report.ReviewedAt = &reviewedAt.Time
	}
	return report, nil
}

func (r *ModerationRepository) queryReports(ctx context.Context, query string, args ...any) ([]domain.ContentReport, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing reports: %w", err)
	}
	defer rows.Close()
	reports := make([]domain.ContentReport, 0)
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

func (r *ModerationRepository) ListReports(ctx context.Context, status string, limit int) ([]domain.ContentReport, error) {
	if status == "" {
		return r.queryReports(ctx, reportSelect+` ORDER BY r.created_at DESC LIMIT $1`, limit)
	}
	return r.queryReports(ctx, reportSelect+` WHERE r.status = $1 ORDER BY r.created_at ASC LIMIT $2`, status, limit)
}

func (r *ModerationRepository) GetReport(ctx context.Context, id string) (*domain.ContentReport, error) {
	report, err := scanReport(r.db.QueryRowContext(ctx, reportSelect+` WHERE r.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrReportNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading report: %w", err)
	}
	return &report, nil
}

func (r *ModerationRepository) ResolveReport(ctx context.Context, id, status string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE content_reports SET status = $2, reviewed_at = NOW() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("resolving report: %w", err)
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return domain.ErrReportNotFound
	}
	return nil
}

func (r *ModerationRepository) RemoveReportedContent(ctx context.Context, report domain.ContentReport) error {
	var err error
	switch report.TargetType {
	case domain.ReportTargetPost:
		_, err = r.db.ExecContext(ctx, `UPDATE posts SET hidden_at = COALESCE(hidden_at, NOW()) WHERE id = $1`, report.TargetID)
	case domain.ReportTargetComment:
		_, err = r.db.ExecContext(ctx, `UPDATE comments SET content = $2 WHERE id = $1`, report.TargetID, removedContentText)
	case domain.ReportTargetMessage:
		_, err = r.db.ExecContext(ctx, `UPDATE messages SET content = $2, type = 'text', metadata = NULL WHERE id = $1`, report.TargetID, removedContentText)
	default:
		return fmt.Errorf("a %s report has no content to remove; suspend the user instead", report.TargetType)
	}
	if err != nil {
		return fmt.Errorf("removing reported %s: %w", report.TargetType, err)
	}
	return nil
}

func (r *ModerationRepository) SuspendUser(ctx context.Context, userID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET suspended_at = COALESCE(suspended_at, NOW()),
		session_version = session_version + 1, updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("suspending user: %w", err)
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return domain.ErrUserNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_devices WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("removing push devices: %w", err)
	}
	return tx.Commit()
}

func (r *ModerationRepository) PendingNotifications(ctx context.Context, limit int) ([]domain.ContentReport, error) {
	return r.queryReports(ctx, reportSelect+` WHERE r.notified_at IS NULL ORDER BY r.created_at ASC LIMIT $1`, limit)
}

func (r *ModerationRepository) MarkNotified(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE content_reports SET notified_at = NOW() WHERE id = $1`, id)
	return err
}

// ---------- Baja de cuenta ----------

// isStorageKey distingue una clave de S3 de una URL externa (por ejemplo, la foto de Google).
func isStorageKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, "://")
}

func (r *ModerationRepository) DeleteAccount(ctx context.Context, userID string, retention time.Duration) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var snapshot domain.DeletedAccountSnapshot
	err = tx.QueryRowContext(ctx, `SELECT email, COALESCE(name, ''), username, dob, document_id, country, avatar_url
		FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, userID).
		Scan(&snapshot.Email, &snapshot.Name, &snapshot.Username, &snapshot.Dob, &snapshot.DocumentID, &snapshot.Country, &snapshot.AvatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("reading account: %w", err)
	}

	keys := make([]string, 0)
	if snapshot.AvatarURL != nil && isStorageKey(*snapshot.AvatarURL) {
		keys = append(keys, *snapshot.AvatarURL)
	}
	var fileKeys []string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(array_agg(k), '{}') FROM (
		SELECT header_image_url AS k FROM posts WHERE author_id = $1 AND header_image_url IS NOT NULL
		UNION SELECT unnest(ARRAY[doc_front_url, doc_back_url, face_url]) FROM kyc_sessions WHERE user_id = $1
	) files WHERE k IS NOT NULL`, userID).Scan(pq.Array(&fileKeys))
	if err != nil {
		return fmt.Errorf("listing account files: %w", err)
	}
	for _, key := range fileKeys {
		if isStorageKey(key) {
			keys = append(keys, key)
		}
	}
	snapshot.ObjectKeys = keys
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO deleted_accounts (user_id, snapshot, purge_after)
			VALUES ($1, $2, NOW() + make_interval(secs => $3))
			ON CONFLICT (user_id) DO UPDATE SET snapshot = EXCLUDED.snapshot, deleted_at = NOW(),
				purge_after = EXCLUDED.purge_after, purged_at = NULL`, []any{userID, payload, retention.Seconds()}},
		// Anonimiza la cuenta: los mensajes, comentarios y transferencias de otros quedan como "Usuario eliminado".
		// El email libre permite volver a registrarse con la misma dirección.
		{`UPDATE users SET name = 'Usuario eliminado',
			username = 'eliminado_' || left(replace(id::text, '-', ''), 12),
			email = 'deleted+' || id::text || '@invalid.root',
			password_hash = '!deleted', avatar_url = NULL, dob = NULL, document_id = NULL,
			followers = '{}', following = '{}', favorite_genres = '{}', is_private = TRUE, is_kyc_verified = FALSE,
			deleted_at = NOW(), session_version = session_version + 1, updated_at = NOW()
			WHERE id = $1`, []any{userID}},
		{`UPDATE users SET followers = array_remove(followers, $1::text), following = array_remove(following, $1::text)
			WHERE $1::text = ANY(followers) OR $1::text = ANY(following)`, []any{userID}},
		{`DELETE FROM follow_requests WHERE requester_id = $1 OR target_id = $1`, []any{userID}},
		{`DELETE FROM user_blocks WHERE blocker_id = $1 OR blocked_id = $1`, []any{userID}},
		{`DELETE FROM push_devices WHERE user_id = $1`, []any{userID}},
		{`DELETE FROM notifications WHERE user_id = $1`, []any{userID}},
		{`DELETE FROM crew_join_requests WHERE user_id = $1 AND status = 'pending'`, []any{userID}},
		{`DELETE FROM event_rrpps WHERE user_id = $1`, []any{userID}},
		{`DELETE FROM community_managers WHERE user_id = $1`, []any{userID}},
		{`UPDATE posts SET hidden_at = NOW() WHERE author_id = $1 AND hidden_at IS NULL`, []any{userID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("deleting account: %w", err)
		}
	}
	return tx.Commit()
}

func (r *ModerationRepository) DuePurges(ctx context.Context, limit int) ([]domain.AccountPurge, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id, snapshot FROM deleted_accounts
		WHERE purged_at IS NULL AND purge_after <= NOW() ORDER BY purge_after LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing due purges: %w", err)
	}
	defer rows.Close()
	purges := make([]domain.AccountPurge, 0)
	for rows.Next() {
		var purge domain.AccountPurge
		var payload []byte
		if err := rows.Scan(&purge.UserID, &payload); err != nil {
			return nil, fmt.Errorf("scanning purge: %w", err)
		}
		var snapshot domain.DeletedAccountSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return nil, fmt.Errorf("decoding snapshot of %s: %w", purge.UserID, err)
		}
		purge.ObjectKeys = snapshot.ObjectKeys
		purges = append(purges, purge)
	}
	return purges, rows.Err()
}

func (r *ModerationRepository) CompletePurge(ctx context.Context, userID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM comments WHERE target_type = 'post' AND target_id IN (SELECT id FROM posts WHERE author_id = $1)`,
		`DELETE FROM posts WHERE author_id = $1`,
		`DELETE FROM kyc_sessions WHERE user_id = $1`,
		`DELETE FROM event_surveys WHERE user_id = $1`,
		`UPDATE deleted_accounts SET snapshot = '{}'::jsonb, purged_at = NOW() WHERE user_id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return fmt.Errorf("purging account %s: %w", userID, err)
		}
	}
	return tx.Commit()
}

// notBlockedSQL es una condición SQL que descarta filas cuyo autor (authorColumn, UUID) y quien mira
// (viewerParam, texto; "" sin sesión) se bloquearon en cualquier sentido.
func notBlockedSQL(authorColumn, viewerParam string) string {
	return `NOT EXISTS (SELECT 1 FROM user_blocks ub WHERE ` + viewerParam + ` <> '' AND (
		(ub.blocker_id::text = ` + viewerParam + ` AND ub.blocked_id = ` + authorColumn + `) OR
		(ub.blocked_id::text = ` + viewerParam + ` AND ub.blocker_id = ` + authorColumn + `)))`
}
