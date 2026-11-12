package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"root-backend-service/internal/core/domain"
	"time"

	"github.com/google/uuid"
)

type NotificationRepository struct{ db *sql.DB }

func NewNotificationRepository(db *sql.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) InitSchema(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`SELECT pg_advisory_xact_lock(hashtextextended('root_notifications_schema',0))`,
		`CREATE TABLE IF NOT EXISTS notification_preferences (
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 messages BOOLEAN NOT NULL DEFAULT TRUE, events BOOLEAN NOT NULL DEFAULT TRUE,
 social BOOLEAN NOT NULL DEFAULT TRUE, crews BOOLEAN NOT NULL DEFAULT TRUE,
 communities BOOLEAN NOT NULL DEFAULT TRUE, activity BOOLEAN NOT NULL DEFAULT TRUE,
 account BOOLEAN NOT NULL DEFAULT TRUE, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE IF NOT EXISTS notifications (
 id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 type TEXT NOT NULL CHECK(length(type) BETWEEN 3 AND 80),
 category TEXT NOT NULL CHECK(category IN ('messages','events','social','crews','communities','activity','account')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 120),
 body TEXT NOT NULL CHECK(length(body) BETWEEN 1 AND 500),
 data JSONB NOT NULL DEFAULT '{}'::jsonb,
 dedupe_key TEXT, scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 read_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE UNIQUE INDEX IF NOT EXISTS notifications_user_dedupe_uidx ON notifications(user_id,dedupe_key) WHERE dedupe_key IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS notifications_user_created_idx ON notifications(user_id,created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS notifications_due_idx ON notifications(scheduled_at)`,
		`CREATE TABLE IF NOT EXISTS notification_deliveries (
 id BIGSERIAL PRIMARY KEY, notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
 device_id UUID NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
 attempts INTEGER NOT NULL DEFAULT 0, done BOOLEAN NOT NULL DEFAULT FALSE,
 available_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(notification_id,device_id))`,
		`CREATE INDEX IF NOT EXISTS notification_deliveries_pending_idx ON notification_deliveries(available_at,id) WHERE NOT done AND attempts<5`,
		`CREATE OR REPLACE FUNCTION root_notification_backfill(p_user UUID,p_device UUID) RETURNS void AS $$
 BEGIN
  DELETE FROM notification_deliveries d USING notifications n
   WHERE d.notification_id=n.id AND d.device_id=p_device AND n.user_id<>p_user;
  INSERT INTO notification_deliveries(notification_id,device_id,available_at)
   SELECT n.id,p_device,n.scheduled_at FROM notifications n WHERE n.user_id=p_user AND n.category<>'messages'
   AND n.scheduled_at>=NOW() AND n.created_at>NOW()-INTERVAL '7 days' ON CONFLICT DO NOTHING;
 END; $$ LANGUAGE plpgsql`,
		`CREATE OR REPLACE FUNCTION root_notification_messages_enabled(p_user UUID) RETURNS boolean AS $$
 BEGIN
  RETURN COALESCE((SELECT messages FROM notification_preferences WHERE user_id=p_user),TRUE);
 END; $$ LANGUAGE plpgsql STABLE`,
		`CREATE OR REPLACE FUNCTION root_notification_delivery_enqueue() RETURNS trigger AS $$
 BEGIN
  IF NEW.category <> 'messages' THEN
   INSERT INTO notification_deliveries(notification_id,device_id,available_at)
   SELECT NEW.id,d.id,NEW.scheduled_at FROM push_devices d WHERE d.user_id=NEW.user_id
   ON CONFLICT DO NOTHING;
  END IF;
  RETURN NEW;
 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS root_notification_delivery_enqueue ON notifications`,
		`CREATE TRIGGER root_notification_delivery_enqueue AFTER INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION root_notification_delivery_enqueue()`,
		`CREATE OR REPLACE FUNCTION root_push_message_enqueue() RETURNS trigger AS $$
 BEGIN
  IF NEW.type IN ('text','image') THEN
   INSERT INTO push_jobs(device_id,user_id,token,message_id)
   SELECT d.id,d.user_id,d.token,NEW.id FROM push_devices d
   JOIN chat_participants cp ON cp.user_id=d.user_id AND cp.chat_id=NEW.chat_id
   WHERE d.user_id<>NEW.sender_id ON CONFLICT DO NOTHING;
   INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
   SELECT gen_random_uuid(),cp.user_id,'chat.message','messages',
    COALESCE(NULLIF(TRIM(sender.name),''),NULLIF(TRIM(sender.username),''),'Nuevo mensaje'),
    CASE WHEN NEW.type='image' THEN '📷 Envió una foto' ELSE LEFT(COALESCE(NULLIF(TRIM(NEW.content),''),'Tenés un mensaje nuevo.'),500) END,
    jsonb_build_object('chat_id',NEW.chat_id::text,'message_id',NEW.id::text),
    'chat.message:'||NEW.id::text||':'||cp.user_id::text,NOW()
   FROM chat_participants cp LEFT JOIN users sender ON sender.id=NEW.sender_id
   WHERE cp.chat_id=NEW.chat_id AND cp.user_id<>NEW.sender_id ON CONFLICT DO NOTHING;
  END IF;
  RETURN NEW;
 END; $$ LANGUAGE plpgsql`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("notification migration: %w", err)
		}
	}
	return tx.Commit()
}

func (r *NotificationRepository) Create(ctx context.Context, input domain.NotificationInput) error {
	if input.UserID == "" || input.Type == "" || input.Title == "" || input.Body == "" {
		return fmt.Errorf("invalid notification input")
	}
	if input.ScheduledAt.IsZero() {
		input.ScheduledAt = time.Now()
	}
	data, err := json.Marshal(input.Data)
	if err != nil {
		return err
	}
	var dedupe any
	if input.DedupeKey != "" {
		dedupe = input.DedupeKey
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, uuid.NewString(), input.UserID,
		input.Type, string(input.Category), input.Title, input.Body, data, dedupe, input.ScheduledAt)
	return err
}

func (r *NotificationRepository) List(ctx context.Context, userID string, limit, offset int) (domain.NotificationPage, error) {
	page := domain.NotificationPage{Data: []domain.Notification{}}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE read_at IS NULL) FROM notifications WHERE user_id=$1 AND scheduled_at<=NOW()`, userID).Scan(&page.Total, &page.UnreadCount); err != nil {
		return page, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,type,category,title,body,data,read_at,scheduled_at,created_at
 FROM notifications WHERE user_id=$1 AND scheduled_at<=NOW() ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.Notification
		var data []byte
		if err := rows.Scan(&item.ID, &item.Type, &item.Category, &item.Title, &item.Body, &data, &item.ReadAt, &item.ScheduledAt, &item.CreatedAt); err != nil {
			return page, err
		}
		item.Data = map[string]string{}
		_ = json.Unmarshal(data, &item.Data)
		page.Data = append(page.Data, item)
	}
	return page, rows.Err()
}

func (r *NotificationRepository) MarkRead(ctx context.Context, userID, notificationID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,NOW()) WHERE id=$1 AND user_id=$2`, notificationID, userID)
	return err
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET read_at=NOW() WHERE user_id=$1 AND read_at IS NULL AND scheduled_at<=NOW()`, userID)
	return err
}

func defaultNotificationPreferences() domain.NotificationPreferences {
	return domain.NotificationPreferences{Messages: true, Events: true, Social: true, Crews: true, Communities: true, Activity: true, Account: true}
}

func (r *NotificationRepository) GetPreferences(ctx context.Context, userID string) (domain.NotificationPreferences, error) {
	prefs := defaultNotificationPreferences()
	err := r.db.QueryRowContext(ctx, `SELECT messages,events,social,crews,communities,activity,account FROM notification_preferences WHERE user_id=$1`, userID).Scan(
		&prefs.Messages, &prefs.Events, &prefs.Social, &prefs.Crews, &prefs.Communities, &prefs.Activity, &prefs.Account)
	if err == sql.ErrNoRows {
		return prefs, nil
	}
	return prefs, err
}

func (r *NotificationRepository) UpdatePreferences(ctx context.Context, userID string, prefs domain.NotificationPreferences) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO notification_preferences(user_id,messages,events,social,crews,communities,activity,account)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(user_id) DO UPDATE SET messages=$2,events=$3,social=$4,crews=$5,communities=$6,activity=$7,account=$8,updated_at=NOW()`,
		userID, prefs.Messages, prefs.Events, prefs.Social, prefs.Crews, prefs.Communities, prefs.Activity, prefs.Account)
	return err
}

func (r *NotificationRepository) ClaimDeliveries(ctx context.Context) ([]domain.PushJob, error) {
	rows, err := r.db.QueryContext(ctx, `WITH next AS (
 SELECT id FROM notification_deliveries WHERE NOT done AND attempts<5 AND available_at<=NOW()
 AND created_at>NOW()-INTERVAL '7 days' ORDER BY available_at,id LIMIT 10 FOR UPDATE SKIP LOCKED
 ) UPDATE notification_deliveries d SET attempts=attempts+1,available_at=NOW()+INTERVAL '2 minutes'
 FROM next,notifications n,push_devices pd WHERE d.id=next.id AND n.id=d.notification_id AND pd.id=d.device_id
 RETURNING d.id,d.attempts,d.device_id,n.user_id,pd.token,pd.platform,n.id,n.type,n.category,n.title,n.body,n.data`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []domain.PushJob{}
	for rows.Next() {
		var job domain.PushJob
		var data []byte
		if err := rows.Scan(&job.ID, &job.Attempt, &job.DeviceID, &job.UserID, &job.Token, &job.Platform,
			&job.NotificationID, &job.NotificationType, &job.Category, &job.Title, &job.Body, &data); err != nil {
			return nil, err
		}
		job.Data = map[string]string{}
		_ = json.Unmarshal(data, &job.Data)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *NotificationRepository) CanSendDelivery(ctx context.Context, job domain.PushJob) (bool, error) {
	var allowed bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM push_devices d JOIN notifications n ON n.id=$4 AND n.user_id=d.user_id
 LEFT JOIN notification_preferences p ON p.user_id=d.user_id
 WHERE d.id=$1 AND d.user_id=$2 AND d.token=$3 AND CASE n.category
  WHEN 'messages' THEN COALESCE(p.messages,TRUE) WHEN 'events' THEN COALESCE(p.events,TRUE)
  WHEN 'social' THEN COALESCE(p.social,TRUE) WHEN 'crews' THEN COALESCE(p.crews,TRUE)
  WHEN 'communities' THEN COALESCE(p.communities,TRUE) WHEN 'activity' THEN COALESCE(p.activity,TRUE)
  WHEN 'account' THEN COALESCE(p.account,TRUE) ELSE TRUE END)`, job.DeviceID, job.UserID, job.Token, job.NotificationID).Scan(&allowed)
	return allowed, err
}

func (r *NotificationRepository) FinishDelivery(ctx context.Context, job domain.PushJob, done bool, retry time.Duration) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notification_deliveries SET done=$3,available_at=NOW()+($4*INTERVAL '1 second') WHERE id=$1 AND attempts=$2`, job.ID, job.Attempt, done, retry.Seconds())
	return err
}

func (r *NotificationRepository) ScheduleEventReminders(ctx context.Context) error {
	statements := []string{
		`INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
 SELECT gen_random_uuid(),r.user_id,'event.today','events','Hoy salís a bailar',
  e.title||' es hoy · '||to_char(e.date AT TIME ZONE 'America/Montevideo','HH24:MI'),
  jsonb_build_object('event_id',e.id::text),'event.today:'||e.id::text||':'||to_char(e.date AT TIME ZONE 'America/Montevideo','YYYY-MM-DD'),
  GREATEST(NOW(),LEAST(e.date-INTERVAL '6 hours',
   ((e.date AT TIME ZONE 'America/Montevideo')::date+TIME '10:00') AT TIME ZONE 'America/Montevideo'))
 FROM event_rsvps r JOIN events e ON e.id=r.event_id WHERE r.status='going' AND e.date>NOW()
 AND (e.date AT TIME ZONE 'America/Montevideo')::date=(NOW() AT TIME ZONE 'America/Montevideo')::date ON CONFLICT DO NOTHING`,
		`INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
 SELECT gen_random_uuid(),r.user_id,'event.starting_soon','events','Tu evento empieza pronto',
  e.title||' empieza a las '||to_char(e.date AT TIME ZONE 'America/Montevideo','HH24:MI')||' en '||e.location,
  jsonb_build_object('event_id',e.id::text),'event.soon:'||e.id::text,NOW()
 FROM event_rsvps r JOIN events e ON e.id=r.event_id WHERE r.status='going' AND e.date>NOW() AND e.date<=NOW()+INTERVAL '3 hours' ON CONFLICT DO NOTHING`,
		`INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
 SELECT gen_random_uuid(),a.user_id,'event.review','events','¿Cómo estuvo esa noche?',
  'Completá tu reseña de '||e.title||' y desbloqueá tu insignia.',jsonb_build_object('event_id',e.id::text),
  'event.review:'||e.id::text,COALESCE(a.departed_at,e.end_date,e.date)+INTERVAL '2 hours'
 FROM event_attendance a JOIN events e ON e.id=a.event_id
 WHERE COALESCE(a.departed_at,e.end_date,e.date)<=NOW() AND COALESCE(a.departed_at,e.end_date,e.date)>NOW()-INTERVAL '48 hours' ON CONFLICT DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("schedule event notifications: %w", err)
		}
	}
	return nil
}

func (r *NotificationRepository) Prune(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notifications WHERE created_at<NOW()-INTERVAL '90 days'`)
	return err
}

func (r *NotificationRepository) NotifyFollow(ctx context.Context, actorID, targetUserID string) error {
	var name, username string
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(TRIM(name),''),username),username FROM users WHERE id=$1`, actorID).Scan(&name, &username); err != nil {
		return err
	}
	return r.Create(ctx, domain.NotificationInput{UserID: targetUserID, Type: "social.follow", Category: domain.NotificationSocial,
		Title: "Nuevo seguidor", Body: name + " empezó a seguirte.", Data: map[string]string{"username": username}, DedupeKey: "follow:" + actorID})
}

func (r *NotificationRepository) NotifyPostLike(ctx context.Context, actorID, postID string) error {
	var owner, name string
	if err := r.db.QueryRowContext(ctx, `SELECT p.author_id,COALESCE(NULLIF(TRIM(u.name),''),u.username) FROM posts p JOIN users u ON u.id=$1 WHERE p.id=$2`, actorID, postID).Scan(&owner, &name); err != nil {
		return err
	}
	if owner == actorID {
		return nil
	}
	return r.Create(ctx, domain.NotificationInput{UserID: owner, Type: "post.like", Category: domain.NotificationSocial,
		Title: "Le gustó tu publicación", Body: name + " marcó que le gusta tu publicación.", Data: map[string]string{"post_id": postID}, DedupeKey: "post.like:" + postID + ":" + actorID})
}

func (r *NotificationRepository) NotifyPostComment(ctx context.Context, actorID, postID, commentID string) error {
	var owner, name string
	if err := r.db.QueryRowContext(ctx, `SELECT p.author_id,COALESCE(NULLIF(TRIM(u.name),''),u.username) FROM posts p JOIN users u ON u.id=$1 WHERE p.id=$2`, actorID, postID).Scan(&owner, &name); err != nil {
		return err
	}
	if owner == actorID {
		return nil
	}
	return r.Create(ctx, domain.NotificationInput{UserID: owner, Type: "post.comment", Category: domain.NotificationSocial,
		Title: "Nuevo comentario", Body: name + " comentó tu publicación.", Data: map[string]string{"post_id": postID}, DedupeKey: "post.comment:" + commentID})
}

func (r *NotificationRepository) NotifyEventComment(ctx context.Context, actorID, eventID, commentID string) error {
	var owner, name, title string
	if err := r.db.QueryRowContext(ctx, `SELECT e.producer_id,COALESCE(NULLIF(TRIM(u.name),''),u.username),e.title FROM events e JOIN users u ON u.id=$1 WHERE e.id=$2 AND e.producer_id IS NOT NULL`, actorID, eventID).Scan(&owner, &name, &title); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if owner == actorID {
		return nil
	}
	return r.Create(ctx, domain.NotificationInput{UserID: owner, Type: "event.comment", Category: domain.NotificationSocial,
		Title: "Comentaron " + title, Body: name + " dejó un comentario en tu evento.", Data: map[string]string{"event_id": eventID}, DedupeKey: "event.comment:" + commentID})
}

func (r *NotificationRepository) NotifyCommunityAnnouncement(ctx context.Context, actorID, communityID, postID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO notifications(id,user_id,type,category,title,body,data,dedupe_key,scheduled_at)
 SELECT gen_random_uuid(),m.user_id,'community.announcement','communities',c.name,
  COALESCE(NULLIF(TRIM(p.title),''),'Hay un nuevo anuncio en la comunidad.'),jsonb_build_object('community_id',c.id::text),
  'community.announcement:'||p.id::text||':'||m.user_id::text,NOW()
 FROM community_members m JOIN communities c ON c.id=m.community_id JOIN posts p ON p.id=$3
 WHERE m.community_id=$2 AND m.user_id<>$1 ON CONFLICT DO NOTHING`, actorID, communityID, postID)
	return err
}

func (r *NotificationRepository) CancelEventReminders(ctx context.Context, userID, eventID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notifications WHERE user_id=$1 AND type IN ('event.today','event.starting_soon') AND data->>'event_id'=$2`, userID, eventID)
	return err
}
