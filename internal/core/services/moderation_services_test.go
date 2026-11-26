package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// ---------- Baja de cuenta ----------

type accountRepoFake struct {
	deleted   []string
	retention time.Duration
	deleteErr error
	purges    []domain.AccountPurge
	completed []string
}

func (f *accountRepoFake) DeleteAccount(_ context.Context, userID string, retention time.Duration) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, userID)
	f.retention = retention
	return nil
}

func (f *accountRepoFake) DuePurges(context.Context, int) ([]domain.AccountPurge, error) {
	return f.purges, nil
}

func (f *accountRepoFake) CompletePurge(_ context.Context, userID string) error {
	f.completed = append(f.completed, userID)
	return nil
}

type deleterFake struct {
	deleted []string
	failOn  string
}

func (d *deleterFake) DeleteObject(_ context.Context, key string) error {
	if key == d.failOn {
		return errors.New("s3 down")
	}
	d.deleted = append(d.deleted, key)
	return nil
}

func TestDeleteAccountKeepsCopyThirtyDaysAndInvalidatesSession(t *testing.T) {
	repo := &accountRepoFake{}
	var invalidated []string
	service := NewAccountService(repo, func(id string) { invalidated = append(invalidated, id) }, nil)

	if err := service.DeleteAccount(context.Background(), "user-1"); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "user-1" || repo.retention != 30*24*time.Hour {
		t.Fatalf("repo got %v with retention %s", repo.deleted, repo.retention)
	}
	if len(invalidated) != 1 || invalidated[0] != "user-1" {
		t.Fatalf("session cache not invalidated: %v", invalidated)
	}
}

func TestDeleteAccountDoesNotInvalidateWhenItFails(t *testing.T) {
	repo := &accountRepoFake{deleteErr: domain.ErrUserNotFound}
	invalidated := false
	service := NewAccountService(repo, func(string) { invalidated = true }, nil)
	if err := service.DeleteAccount(context.Background(), "user-1"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("err = %v", err)
	}
	if invalidated {
		t.Fatal("session invalidated for a failed deletion")
	}
}

func TestPurgeDueDeletesFilesBeforeData(t *testing.T) {
	repo := &accountRepoFake{purges: []domain.AccountPurge{
		{UserID: "ok", ObjectKeys: []string{"avatars/ok/a.jpg", "kyc/s1/face.jpg"}},
		{UserID: "fails", ObjectKeys: []string{"posts/fails/broken.jpg"}},
		{UserID: "no-files"},
	}}
	files := &deleterFake{failOn: "posts/fails/broken.jpg"}
	service := NewAccountService(repo, nil, files)

	n, err := service.PurgeDue(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fails") {
		t.Fatalf("expected the failing account in the error, got %v", err)
	}
	if n != 2 || strings.Join(repo.completed, ",") != "ok,no-files" {
		t.Fatalf("purged %d: %v", n, repo.completed)
	}
	if strings.Join(files.deleted, ",") != "avatars/ok/a.jpg,kyc/s1/face.jpg" {
		t.Fatalf("deleted files %v", files.deleted)
	}
}

func TestPurgeDueWaitsForStorage(t *testing.T) {
	repo := &accountRepoFake{purges: []domain.AccountPurge{{UserID: "u", ObjectKeys: []string{"avatars/u/a.jpg"}}}}
	n, err := NewAccountService(repo, nil, nil).PurgeDue(context.Background())
	if err == nil || n != 0 || len(repo.completed) != 0 {
		t.Fatalf("without storage nothing must be purged: n=%d err=%v completed=%v", n, err, repo.completed)
	}
}

// ---------- Bloqueos ----------

type blockRepoFake struct {
	ports.BlockRepository
	blocked, unblocked [][2]string
}

func (f *blockRepoFake) Block(_ context.Context, blocker, blocked string) error {
	f.blocked = append(f.blocked, [2]string{blocker, blocked})
	return nil
}

func (f *blockRepoFake) Unblock(_ context.Context, blocker, blocked string) (bool, error) {
	f.unblocked = append(f.unblocked, [2]string{blocker, blocked})
	return false, nil
}

type usernameRepoFake struct {
	ports.UserRepository
	users map[string]string
}

func (f *usernameRepoFake) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	id, ok := f.users[username]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return &domain.User{ID: id, Username: username}, nil
}

func TestBlockResolvesUsernameAndRejectsSelf(t *testing.T) {
	blocks := &blockRepoFake{}
	service := NewBlockService(blocks, &usernameRepoFake{users: map[string]string{"ana": "ana-id", "me": "me-id"}})

	if err := service.Block(context.Background(), "me-id", "  Ana "); err != nil {
		t.Fatal(err)
	}
	if len(blocks.blocked) != 1 || blocks.blocked[0] != [2]string{"me-id", "ana-id"} {
		t.Fatalf("blocked %v", blocks.blocked)
	}
	if err := service.Block(context.Background(), "me-id", "me"); !errors.Is(err, domain.ErrCannotBlockSelf) {
		t.Fatalf("self block err = %v", err)
	}
	if err := service.Block(context.Background(), "me-id", "ghost"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
	// Desbloquear a alguien que no estaba bloqueado no es un error.
	if err := service.Unblock(context.Background(), "me-id", "ana"); err != nil {
		t.Fatal(err)
	}
}

// ---------- Denuncias ----------

type reportRepoFake struct {
	ports.ReportRepository
	created  []domain.ReportInput
	pending  []domain.ContentReport
	notified []string
}

func (f *reportRepoFake) CreateReport(_ context.Context, _ string, input domain.ReportInput) (string, bool, error) {
	f.created = append(f.created, input)
	return "report-1", true, nil
}

func (f *reportRepoFake) PendingNotifications(context.Context, int) ([]domain.ContentReport, error) {
	return f.pending, nil
}

func (f *reportRepoFake) MarkNotified(_ context.Context, id string) error {
	f.notified = append(f.notified, id)
	return nil
}

type notifierFake struct {
	sent   []string
	failOn string
}

func (n *notifierFake) SendReportNotification(_ context.Context, report domain.ContentReport) error {
	if report.ID == n.failOn {
		return errors.New("provider down")
	}
	n.sent = append(n.sent, report.ID)
	return nil
}

func TestCreateReportValidatesInput(t *testing.T) {
	const target = "6f1c1d2e-3b4a-4c5d-8e9f-0a1b2c3d4e5f"
	cases := []struct {
		name  string
		input domain.ReportInput
		ok    bool
	}{
		{"valid", domain.ReportInput{TargetType: domain.ReportTargetMessage, TargetID: target, Reason: "abuse", Details: "  insultos  "}, true},
		{"unknown type", domain.ReportInput{TargetType: "event", TargetID: target, Reason: "spam"}, false},
		{"bad id", domain.ReportInput{TargetType: domain.ReportTargetPost, TargetID: "1", Reason: "spam"}, false},
		{"bad reason", domain.ReportInput{TargetType: domain.ReportTargetUser, TargetID: target, Reason: "boring"}, false},
		{"long details", domain.ReportInput{TargetType: domain.ReportTargetComment, TargetID: target, Reason: "other", Details: strings.Repeat("á", 1001)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &reportRepoFake{}
			_, _, err := NewReportService(repo, nil).Create(context.Background(), "reporter", tc.input)
			if tc.ok {
				if err != nil || len(repo.created) != 1 || repo.created[0].Details != "insultos" {
					t.Fatalf("err=%v created=%v", err, repo.created)
				}
				return
			}
			if !errors.Is(err, domain.ErrInvalidReport) || len(repo.created) != 0 {
				t.Fatalf("err=%v created=%v", err, repo.created)
			}
		})
	}
}

func TestProcessNotificationsRetriesFailedEmails(t *testing.T) {
	repo := &reportRepoFake{pending: []domain.ContentReport{{ID: "a"}, {ID: "b"}}}
	notifier := &notifierFake{failOn: "b"}
	err := NewReportService(repo, notifier).ProcessNotifications(context.Background())
	if err == nil {
		t.Fatal("expected the failed email to be reported")
	}
	// Solo "a" queda marcada: "b" se vuelve a intentar en la próxima vuelta.
	if strings.Join(repo.notified, ",") != "a" || strings.Join(notifier.sent, ",") != "a" {
		t.Fatalf("notified=%v sent=%v", repo.notified, notifier.sent)
	}
}

func TestProcessNotificationsWithoutNotifierIsNoop(t *testing.T) {
	repo := &reportRepoFake{pending: []domain.ContentReport{{ID: "a"}}}
	if err := NewReportService(repo, nil).ProcessNotifications(context.Background()); err != nil || len(repo.notified) != 0 {
		t.Fatalf("err=%v notified=%v", err, repo.notified)
	}
}

// ---------- Chat con bloqueos ----------

type chatRepoBlockFake struct {
	ports.ChatRepository
	chat *domain.Chat
}

func (f *chatRepoBlockFake) GetChatByID(context.Context, string) (*domain.Chat, error) {
	return f.chat, nil
}

type messageRepoBlockFake struct {
	ports.MessageRepository
	created int
}

func (f *messageRepoBlockFake) CreateMessage(context.Context, *domain.Message) error {
	f.created++
	return nil
}

type blockStateFake struct {
	ports.BlockRepository
	byViewer, byOther bool
}

func (f blockStateFake) BlockState(context.Context, string, string) (bool, bool, error) {
	return f.byViewer, f.byOther, nil
}

func TestDirectChatRespectsBlocksBothWays(t *testing.T) {
	for _, state := range []blockStateFake{{byViewer: true}, {byOther: true}} {
		chats := &chatRepoBlockFake{chat: &domain.Chat{ID: "c", Type: domain.ChatTypeDirect,
			Participants: []domain.User{{ID: "me"}, {ID: "other"}}}}
		messages := &messageRepoBlockFake{}
		service := NewChatService(chats, messages)
		UseChatBlocks(service, state)

		if _, err := service.SendMessage(context.Background(), "c", "me", "hola", domain.MessageTypeText); !errors.Is(err, domain.ErrBlocked) {
			t.Fatalf("send err = %v", err)
		}
		if _, err := service.GetOrCreateDirectChat(context.Background(), "me", "other"); !errors.Is(err, domain.ErrBlocked) {
			t.Fatalf("create err = %v", err)
		}
		if messages.created != 0 {
			t.Fatal("a message was stored despite the block")
		}
	}
}

func TestCrewChatIgnoresBlocks(t *testing.T) {
	chats := &chatRepoBlockFake{chat: &domain.Chat{ID: "c", Type: domain.ChatTypeCrews,
		Participants: []domain.User{{ID: "me"}, {ID: "other"}}}}
	messages := &messageRepoBlockFake{}
	service := NewChatService(chats, messages)
	UseChatBlocks(service, blockStateFake{byViewer: true})
	if _, err := service.SendMessage(context.Background(), "c", "me", "hola", domain.MessageTypeText); err != nil || messages.created != 1 {
		t.Fatalf("crew message err=%v created=%d", err, messages.created)
	}
}

// ---------- Moderación de posts ----------

type moderatorFake struct{ reject string }

func (m moderatorFake) ValidateContent(_ context.Context, content string) error {
	if strings.Contains(content, m.reject) {
		return domain.ErrContentPolicyViolation
	}
	return nil
}

type postRepoModerationFake struct {
	ports.PostRepository
	created int
}

func (f *postRepoModerationFake) CreatePost(context.Context, *domain.Post) error {
	f.created++
	return nil
}

func (f *postRepoModerationFake) CreatePostComment(context.Context, string, string, string) (*domain.EventComment, error) {
	f.created++
	return &domain.EventComment{ID: "c"}, nil
}

func TestPostModeratorRejectsBeforeSaving(t *testing.T) {
	repo := &postRepoModerationFake{}
	service := NewPostService(repo, nil)
	UsePostModerator(service, moderatorFake{reject: "odio"})

	title := "Fiesta"
	if err := service.CreatePost(context.Background(), &domain.Post{Title: &title, Content: "mensaje de odio"}); !errors.Is(err, domain.ErrContentPolicyViolation) {
		t.Fatalf("post err = %v", err)
	}
	if _, err := service.CreatePostComment(context.Background(), "p", "a", "odio"); !errors.Is(err, domain.ErrContentPolicyViolation) {
		t.Fatalf("comment err = %v", err)
	}
	if repo.created != 0 {
		t.Fatal("rejected content was stored")
	}
	if err := service.CreatePost(context.Background(), &domain.Post{Content: "nos vemos el sábado"}); err != nil || repo.created != 1 {
		t.Fatalf("valid post err=%v created=%d", err, repo.created)
	}
}
