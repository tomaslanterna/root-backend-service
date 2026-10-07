package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"root-backend-service/internal/core/domain"
)

func communityAffected(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("update community content: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("community affected rows: %w", err)
	}
	if n == 0 {
		return domain.ErrCommunityTargetNotFound
	}
	return nil
}

func (r *communityRepository) SetMuted(ctx context.Context, id, userID string, muted bool) error {
	return communityAffected(r.db.ExecContext(ctx, `UPDATE community_members SET muted=$3 WHERE community_id=$1::uuid AND user_id=$2::uuid`, id, userID, muted))
}

func (r *communityRepository) MarkRead(ctx context.Context, id, userID, postID string) error {
	// Compare timestamp AND ID: equal timestamps and out-of-order requests cannot lose unread announcements.
	return communityAffected(r.db.ExecContext(ctx, `UPDATE community_members m
	 SET last_read_at=CASE WHEN (p.timestamp,p.id) > (COALESCE(m.last_read_at,m.joined_at), COALESCE(m.last_read_post_id,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid)) THEN p.timestamp ELSE m.last_read_at END,
	 last_read_post_id=CASE WHEN (p.timestamp,p.id) > (COALESCE(m.last_read_at,m.joined_at), COALESCE(m.last_read_post_id,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid)) THEN p.id ELSE m.last_read_post_id END
	 FROM posts p WHERE m.community_id=$1::uuid AND m.user_id=$2::uuid AND p.id=$3::uuid AND p.community_id=m.community_id`, id, userID, postID))
}

func (r *communityRepository) SetPinned(ctx context.Context, id, postID string, pinned bool) error {
	return communityAffected(r.db.ExecContext(ctx, `UPDATE posts SET is_pinned=$3 WHERE community_id=$1::uuid AND id=$2::uuid`, id, postID, pinned))
}

func (r *communityRepository) CreateReport(ctx context.Context, id, userID string, input domain.CommunityReportInput) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin community report: %w", err)
	}
	defer tx.Rollback()
	var content string
	err = tx.QueryRowContext(ctx, `SELECT content FROM (
	 SELECT p.content FROM posts p WHERE $2='post' AND p.id=$3::uuid AND p.community_id=$1::uuid
	 UNION ALL SELECT cm.content FROM comments cm JOIN posts p ON cm.target_id=p.id
	 WHERE $2='comment' AND cm.id=$3::uuid AND cm.target_type='post' AND p.community_id=$1::uuid
	) target`, id, input.TargetType, input.TargetID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrCommunityTargetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve report content: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO community_reports(community_id,reporter_id,target_type,target_id,reason,details,content_snapshot)
	 VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7) ON CONFLICT(reporter_id,target_type,target_id) DO NOTHING`, id, userID, input.TargetType, input.TargetID, input.Reason, input.Details, content)
	if err != nil {
		return "", fmt.Errorf("save community report: %w", err)
	}
	var reportID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM community_reports WHERE reporter_id=$1::uuid AND target_type=$2 AND target_id=$3::uuid AND community_id=$4::uuid`, userID, input.TargetType, input.TargetID, id).Scan(&reportID); err != nil {
		return "", fmt.Errorf("get saved community report: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit community report: %w", err)
	}
	return reportID, nil
}

func (r *communityRepository) GetReports(ctx context.Context, id string, limit, offset int) ([]domain.CommunityReport, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM community_reports WHERE community_id=$1::uuid AND status='pending'`, id).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reports: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT cr.id,cr.target_type,cr.target_id,cr.reason,cr.details,cr.content_snapshot,COALESCE(u.name,'Usuario'),cr.status,cr.created_at
	 FROM community_reports cr LEFT JOIN users u ON u.id=cr.reporter_id WHERE cr.community_id=$1::uuid AND cr.status='pending' ORDER BY cr.created_at DESC,cr.id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	reports := make([]domain.CommunityReport, 0)
	for rows.Next() {
		var report domain.CommunityReport
		if err := rows.Scan(&report.ID, &report.TargetType, &report.TargetID, &report.Reason, &report.Details, &report.Content, &report.ReporterName, &report.Status, &report.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate reports: %w", err)
	}
	return reports, total, nil
}

func (r *communityRepository) ReviewReport(ctx context.Context, id, userID, reportID, status string) error {
	return communityAffected(r.db.ExecContext(ctx, `UPDATE community_reports SET status=$4,reviewer_id=$2::uuid,reviewed_at=NOW() WHERE community_id=$1::uuid AND id=$3::uuid`, id, userID, reportID, status))
}
