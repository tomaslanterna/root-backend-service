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

const (
	hostUser    = "11111111-1111-1111-1111-111111111111"
	guestUser   = "22222222-2222-2222-2222-222222222222"
	afterIDTest = "33333333-3333-3333-3333-333333333333"
	eventIDTest = "44444444-4444-4444-4444-444444444444"
)

type fakeAccess map[string]domain.ViewerAccess

func (f fakeAccess) ViewerAccess(_ context.Context, id string) (domain.ViewerAccess, error) {
	v, ok := f[id]
	if !ok {
		return v, domain.ErrUserNotFound
	}
	return v, nil
}

type fakeAfterRepo struct {
	ports.AfterRepository
	row       *domain.AfterDetailRow
	current   domain.AfterCurrent
	window    *domain.AfterEventWindow
	createErr error
	created   *domain.AfterFields
	updated   *domain.AfterFields
	notice    domain.NoticeChange
	joined    bool
}

func (f *fakeAfterRepo) Detail(context.Context, string, string) (*domain.AfterDetailRow, error) {
	if f.row == nil {
		return nil, domain.ErrAfterNotFound
	}
	copy := *f.row
	return &copy, nil
}

func (f *fakeAfterRepo) EventWindow(context.Context, string) (domain.AfterEventWindow, error) {
	if f.window == nil {
		return domain.AfterEventWindow{}, domain.ErrEventNotFound
	}
	return *f.window, nil
}

func (f *fakeAfterRepo) Create(_ context.Context, _ string, fields domain.AfterFields, _ time.Time) (string, error) {
	f.created = &fields
	return afterIDTest, f.createErr
}

func (f *fakeAfterRepo) Current(context.Context, string, time.Time) (domain.AfterCurrent, error) {
	return f.current, nil
}

func (f *fakeAfterRepo) Update(_ context.Context, _, _ string, fields domain.AfterFields, notice domain.NoticeChange) error {
	f.updated, f.notice = &fields, notice
	return nil
}

func (f *fakeAfterRepo) Join(context.Context, string, string, *string) (string, error) {
	f.joined = true
	return domain.AttendeeGoing, nil
}

type recordingModerator struct{ texts []string }

func (m *recordingModerator) ValidateContent(_ context.Context, text string) error {
	m.texts = append(m.texts, text)
	if strings.Contains(text, "prohibido") {
		return domain.ErrContentPolicyViolation
	}
	return nil
}

var fixedNow = time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)

func newTestAfterService(repo *fakeAfterRepo, access fakeAccess) (*afterService, *recordingModerator) {
	moderator := &recordingModerator{}
	svc := NewAfterService(repo, access).(*afterService)
	svc.now = func() time.Time { return fixedNow }
	UseAfterModerator(svc, moderator)
	return svc, moderator
}

func adultAccess(kyc bool) domain.ViewerAccess {
	return domain.ViewerAccess{IsKycVerified: kyc, Dob: "1995-01-01"}
}

func validFields() domain.AfterFields {
	return domain.AfterFields{Name: "After", StartsAt: fixedNow.Add(time.Hour), Zone: "Pocitos", Address: "Calle secreta 123", Access: domain.AfterAccessApproval}
}

func detailRow(access, status string, archived bool, viewer *string) *domain.AfterDetailRow {
	row := &domain.AfterDetailRow{Address: "Calle secreta 123", StoredStatus: status, Archived: archived}
	row.Detail.ID, row.Detail.Access, row.Detail.ViewerStatus = afterIDTest, access, viewer
	row.Detail.Host.UserID = hostUser
	return row
}

func TestAfterKycIsRequiredToCreateAndEdit(t *testing.T) {
	for name, access := range map[string]fakeAccess{
		"sin verificar": {hostUser: adultAccess(false)},
		"suspendido":    {hostUser: {IsKycVerified: true, Dob: "1990-01-01", Unavailable: true}},
		"no existe":     {},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeAfterRepo{current: domain.AfterCurrent{HostID: hostUser, Fields: validFields()}}
			svc, _ := newTestAfterService(repo, access)
			if _, err := svc.Create(context.Background(), hostUser, validFields()); !errors.Is(err, domain.ErrKycRequired) {
				t.Fatalf("create: %v", err)
			}
			if _, err := svc.Update(context.Background(), afterIDTest, hostUser, domain.AfterPatch{}); !errors.Is(err, domain.ErrKycRequired) {
				t.Fatalf("update: %v", err)
			}
			if repo.created != nil || repo.updated != nil {
				t.Fatal("nothing is written without identity verification")
			}
		})
	}
}

func TestAfterCreateModeratesPublicTextButNeverTheAddress(t *testing.T) {
	repo := &fakeAfterRepo{row: detailRow(domain.AfterAccessApproval, domain.AfterStatusActive, false, ptrTo(domain.ViewerStatusHost))}
	svc, moderator := newTestAfterService(repo, fakeAccess{hostUser: adultAccess(true)})
	fields := validFields()
	fields.Rules = ptrTo("Sin drama")
	detail, err := svc.Create(context.Background(), hostUser, fields)
	if err != nil || detail.Address == nil || !detail.CanEdit {
		t.Fatalf("the host sees the address of the new after: %+v %v", detail, err)
	}
	for _, text := range moderator.texts {
		if strings.Contains(text, "Calle secreta") {
			t.Fatal("the address must never reach the moderator")
		}
	}
	fields.Name = "prohibido"
	if _, err := svc.Create(context.Background(), hostUser, fields); !errors.Is(err, domain.ErrContentPolicyViolation) {
		t.Fatalf("moderation: %v", err)
	}
}

func TestAfterCreateWithUnknownEventIsInvalidEvent(t *testing.T) {
	repo := &fakeAfterRepo{}
	svc, _ := newTestAfterService(repo, fakeAccess{hostUser: adultAccess(true)})
	fields := validFields()
	fields.EventID = ptrTo(eventIDTest)
	if _, err := svc.Create(context.Background(), hostUser, fields); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatalf("unknown event: %v", err)
	}
	fields.EventID = ptrTo("not-a-uuid")
	if _, err := svc.Create(context.Background(), hostUser, fields); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatalf("malformed event id: %v", err)
	}
	repo.createErr = domain.ErrTooManyActiveAfters
	if _, err := svc.Create(context.Background(), hostUser, validFields()); !errors.Is(err, domain.ErrTooManyActiveAfters) {
		t.Fatalf("the active limit from the repository reaches the caller: %v", err)
	}
}

func TestAfterDetailAgeAndVisibility(t *testing.T) {
	guest := ptrTo(domain.AttendeeRequested)
	for _, tc := range []struct {
		name   string
		viewer string
		access fakeAccess
		row    *domain.AfterDetailRow
		want   error
	}{
		{"menor", guestUser, fakeAccess{guestUser: {Dob: "2012-05-05"}}, detailRow(domain.AfterAccessFree, domain.AfterStatusActive, false, nil), domain.ErrUnderage},
		{"sin fecha de nacimiento", guestUser, fakeAccess{guestUser: {Dob: ""}}, detailRow(domain.AfterAccessFree, domain.AfterStatusActive, false, nil), domain.ErrUnderage},
		{"cuenta suspendida", guestUser, fakeAccess{guestUser: {Dob: "1990-01-01", Unavailable: true}}, detailRow(domain.AfterAccessFree, domain.AfterStatusActive, false, nil), domain.ErrUnderage},
		{"adulto ve uno activo", guestUser, fakeAccess{guestUser: adultAccess(false)}, detailRow(domain.AfterAccessApproval, domain.AfterStatusActive, false, guest), nil},
		{"oculto: 404 para otros", guestUser, fakeAccess{guestUser: adultAccess(false)}, detailRow(domain.AfterAccessFree, domain.AfterStatusHidden, false, nil), domain.ErrAfterNotFound},
		{"archivado: 404 para otros", guestUser, fakeAccess{guestUser: adultAccess(false)}, detailRow(domain.AfterAccessFree, domain.AfterStatusActive, true, nil), domain.ErrAfterNotFound},
		{"eliminado: 404 también para el host", hostUser, fakeAccess{}, detailRow(domain.AfterAccessFree, domain.AfterStatusRemoved, false, ptrTo(domain.ViewerStatusHost)), domain.ErrAfterNotFound},
		{"oculto: el host lo ve", hostUser, fakeAccess{}, detailRow(domain.AfterAccessFree, domain.AfterStatusHidden, false, ptrTo(domain.ViewerStatusHost)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestAfterService(&fakeAfterRepo{row: tc.row}, tc.access)
			detail, err := svc.Detail(context.Background(), afterIDTest, tc.viewer)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if err == nil && tc.row.Detail.Access == domain.AfterAccessApproval && tc.viewer == guestUser && detail.Address != nil {
				t.Fatal("a pending request never gets the address")
			}
		})
	}
}

func TestAfterJoinRequiresAdult(t *testing.T) {
	repo := &fakeAfterRepo{row: detailRow(domain.AfterAccessFree, domain.AfterStatusActive, false, ptrTo(domain.AttendeeGoing))}
	svc, _ := newTestAfterService(repo, fakeAccess{guestUser: {Dob: "2010-01-01", IsKycVerified: true}})
	if _, err := svc.Join(context.Background(), afterIDTest, guestUser, ""); !errors.Is(err, domain.ErrUnderage) || repo.joined {
		t.Fatalf("an underage user cannot join: %v joined=%v", err, repo.joined)
	}
	svc, _ = newTestAfterService(repo, fakeAccess{guestUser: adultAccess(false)})
	detail, err := svc.Join(context.Background(), afterIDTest, guestUser, "hola")
	if err != nil || !repo.joined || detail.Address == nil || !detail.AddressVisible {
		t.Fatalf("joining a free after (no KYC needed) shows the address: %+v %v", detail, err)
	}
	if _, err := svc.Join(context.Background(), afterIDTest, guestUser, strings.Repeat("x", 281)); err == nil {
		t.Fatal("message is limited to 280 characters")
	}
}

func TestAfterUpdateOnlyHostAndNoticeChange(t *testing.T) {
	started := validFields()
	started.StartsAt = fixedNow.Add(-2 * time.Hour) // ya empezó: si no cambia, no se revalida
	repo := &fakeAfterRepo{
		current: domain.AfterCurrent{HostID: hostUser, Fields: started, StoredStatus: domain.AfterStatusActive},
		row:     detailRow(domain.AfterAccessApproval, domain.AfterStatusActive, false, ptrTo(domain.ViewerStatusHost)),
	}
	access := fakeAccess{hostUser: adultAccess(true), guestUser: adultAccess(true)}
	svc, moderator := newTestAfterService(repo, access)
	if _, err := svc.Update(context.Background(), afterIDTest, guestUser, domain.AfterPatch{}); !errors.Is(err, domain.ErrNotAfterHost) {
		t.Fatalf("only the host edits: %v", err)
	}
	notice := "Tocá timbre 2"
	if _, err := svc.Update(context.Background(), afterIDTest, hostUser, domain.AfterPatch{Notice: domain.Nullable[string]{Set: true, Value: &notice}}); err != nil {
		t.Fatalf("update notice: %v", err)
	}
	if !repo.notice.Changed || repo.notice.Value == nil || *repo.notice.Value != notice || repo.updated == nil {
		t.Fatalf("notice change: %+v", repo.notice)
	}
	if len(moderator.texts) != 1 || moderator.texts[0] != notice {
		t.Fatalf("only the changed text is moderated: %v", moderator.texts)
	}
	repo.current.Archived = true
	if _, err := svc.Update(context.Background(), afterIDTest, hostUser, domain.AfterPatch{}); !errors.Is(err, domain.ErrAfterEnded) {
		t.Fatalf("an archived after cannot be edited: %v", err)
	}
}

func TestAfterAdminRequiresRole(t *testing.T) {
	svc, _ := newTestAfterService(&fakeAfterRepo{}, fakeAccess{guestUser: {Role: "USER"}, hostUser: {Role: "RRPP"}})
	for _, user := range []string{guestUser, hostUser, "55555555-5555-5555-5555-555555555555"} {
		if _, err := svc.AdminList(context.Background(), user, "hidden", 10, 0); !errors.Is(err, domain.ErrAdminRequired) {
			t.Fatalf("%s: %v", user, err)
		}
		if err := svc.AdminSetStatus(context.Background(), user, afterIDTest, domain.AfterStatusActive); !errors.Is(err, domain.ErrAdminRequired) {
			t.Fatalf("%s: %v", user, err)
		}
	}
}

type fakeSubmissionRepo struct {
	ports.EventSubmissionRepository
	created bool
}

func (f *fakeSubmissionRepo) Create(context.Context, string, domain.EventSubmissionInput) (domain.EventSubmission, error) {
	f.created = true
	return domain.EventSubmission{ID: "s"}, nil
}

func (f *fakeSubmissionRepo) Publish(context.Context, string, string) (string, error) {
	return "ev", nil
}

func TestEventSubmissionAccess(t *testing.T) {
	input := domain.EventSubmissionInput{Name: "Fiesta", StartsAt: time.Now().Add(48 * time.Hour), VenueName: "Club", VenueAddress: "Calle 1",
		FlyerKey: "event-submissions/" + guestUser + "/1.jpg", SubmitterRole: "fan", Contact: "@yo", RightsConfirmed: true}
	repo := &fakeSubmissionRepo{}
	svc := NewEventSubmissionService(repo, fakeAccess{guestUser: adultAccess(false), hostUser: {IsKycVerified: true, Role: "ADMIN"}})
	if _, err := svc.Create(context.Background(), guestUser, input); !errors.Is(err, domain.ErrKycRequired) || repo.created {
		t.Fatalf("an unverified user cannot submit: %v", err)
	}
	if _, err := svc.Publish(context.Background(), guestUser, afterIDTest); !errors.Is(err, domain.ErrAdminRequired) {
		t.Fatalf("only admins publish: %v", err)
	}
	if id, err := svc.Publish(context.Background(), hostUser, afterIDTest); err != nil || id != "ev" {
		t.Fatalf("admin publishes: %v", err)
	}
	if list, err := svc.Similar(context.Background(), "ab", nil); err != nil || len(list) != 0 {
		t.Fatalf("short names do not search: %v %v", list, err)
	}
}

func ptrTo[T any](v T) *T { return &v }
