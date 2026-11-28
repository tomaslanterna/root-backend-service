package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestAgeOn(t *testing.T) {
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC) // 12:00 en Montevideo
	for _, tc := range []struct {
		name  string
		dob   string
		age   int
		ok    bool
		adult bool
	}{
		{"cumple 18 hoy", "2008-11-27", 18, true, true},
		{"cumple 18 mañana", "2008-11-28", 17, true, false},
		{"adulto", "1995-03-10", 31, true, true},
		{"con hora", "1995-03-10T00:00:00Z", 31, true, true},
		{"vacío: no verificable", "", 0, false, false},
		{"otro formato: no verificable", "27/11/1990", 0, false, false},
		{"basura", "1990-13-45", 0, false, false},
		{"futuro", "2030-01-01", 0, false, false},
		{"sufijo raro", "1990-01-01xyz", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			age, ok := AgeOn(tc.dob, now)
			if ok != tc.ok || (ok && age != tc.age) {
				t.Fatalf("AgeOn(%q) = %d,%v; want %d,%v", tc.dob, age, ok, tc.age, tc.ok)
			}
			if IsAdult(tc.dob, now) != tc.adult {
				t.Fatalf("IsAdult(%q) = %v", tc.dob, !tc.adult)
			}
		})
	}
	// En Montevideo ya es el 27 aunque en UTC todavía sea 27 temprano: el cumpleaños se cuenta en hora local.
	lateUTC := time.Date(2026, 11, 28, 2, 0, 0, 0, time.UTC) // 23:00 del 27 en Montevideo
	if IsAdult("2008-11-28", lateUTC) {
		t.Fatal("at 23:00 of the 27th in Montevideo, someone born on the 28th is still 17")
	}
}

func TestAfterAddressAndNoticeVisibility(t *testing.T) {
	notice := "Traé tu vaso"
	for _, tc := range []struct {
		name        string
		access      string
		viewer      *string
		wantAddress bool
		wantNotice  bool
		wantEdit    bool
	}{
		{"approval: visitante", AfterAccessApproval, nil, false, false, false},
		{"approval: pidió", AfterAccessApproval, ptr(AttendeeRequested), false, false, false},
		{"approval: rechazado", AfterAccessApproval, ptr(AttendeeRejected), false, false, false},
		{"approval: aceptado", AfterAccessApproval, ptr(AttendeeAccepted), true, true, false},
		{"approval: host", AfterAccessApproval, ptr(ViewerStatusHost), true, true, true},
		{"free: visitante ve la dirección, no el aviso", AfterAccessFree, nil, true, false, false},
		{"free: va", AfterAccessFree, ptr(AttendeeGoing), true, true, false},
		{"ticket: visitante", AfterAccessTicket, nil, true, false, false},
		{"ticket: va", AfterAccessTicket, ptr(AttendeeGoing), true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := AfterDetailRow{Address: "Calle Falsa 123", Notice: &notice, StoredStatus: AfterStatusActive}
			row.Detail.Access = tc.access
			row.Detail.ViewerStatus = tc.viewer
			got := row.ForViewer()
			if got.AddressVisible != tc.wantAddress || (got.Address != nil) != tc.wantAddress {
				t.Fatalf("address: visible=%v address=%v", got.AddressVisible, got.Address)
			}
			if (got.Notice != nil) != tc.wantNotice || got.CanEdit != tc.wantEdit {
				t.Fatalf("notice=%v canEdit=%v", got.Notice, got.CanEdit)
			}
			if got.Genres == nil || got.FollowedGoing.Preview == nil {
				t.Fatal("lists must never be null")
			}
		})
	}
	archived := AfterDetailRow{StoredStatus: AfterStatusActive, Archived: true}
	archived.Detail.ViewerStatus = ptr(ViewerStatusHost)
	if got := archived.ForViewer(); got.Status != AfterStatusArchived || got.CanEdit {
		t.Fatalf("an archived after is shown as archived and cannot be edited: %+v", got)
	}
}

func TestNormalizeAfter(t *testing.T) {
	now := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	host := "11111111-1111-1111-1111-111111111111"
	base := func() AfterFields {
		return AfterFields{Name: " After en lo de Juli ", StartsAt: now.Add(2 * time.Hour), Zone: "Pocitos",
			Address: "Calle 1234", Access: AfterAccessFree, Genres: []string{"Techno", "techno", " House "}}
	}
	event := &AfterEventWindow{ID: "ev", StartsAt: now.Add(time.Hour), EndsAt: now.Add(7 * time.Hour)}
	check := AfterValidation{Now: now, HostID: host, CheckStart: true, CheckEventTo: true}

	ok, err := NormalizeAfter(base(), check)
	if err != nil || ok.Name != "After en lo de Juli" || len(ok.Genres) != 2 {
		t.Fatalf("valid after: %+v %v", ok, err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*AfterFields, *AfterValidation)
		want   string // texto del ValidationError, o "invalid_event"
	}{
		{"sin nombre", func(f *AfterFields, _ *AfterValidation) { f.Name = "  " }, "name"},
		{"nombre largo", func(f *AfterFields, _ *AfterValidation) { f.Name = strings.Repeat("a", 61) }, "name"},
		{"sin dirección", func(f *AfterFields, _ *AfterValidation) { f.Address = "" }, "address"},
		{"acceso inválido", func(f *AfterFields, _ *AfterValidation) { f.Access = "vip" }, "access"},
		{"ticket sin link", func(f *AfterFields, _ *AfterValidation) { f.Access = AfterAccessTicket }, "ticketUrl is required"},
		{"ticket con http", func(f *AfterFields, _ *AfterValidation) {
			f.Access, f.TicketURL = AfterAccessTicket, ptr("http://entradas.example/x")
		}, "https"},
		{"cupo 1", func(f *AfterFields, _ *AfterValidation) { f.Capacity = ptr(1) }, "capacity"},
		{"cupo 501", func(f *AfterFields, _ *AfterValidation) { f.Capacity = ptr(501) }, "capacity"},
		{"seis géneros", func(f *AfterFields, _ *AfterValidation) { f.Genres = []string{"a", "b", "c", "d", "e", "f"} }, "genres"},
		{"empieza hace una hora", func(f *AfterFields, _ *AfterValidation) { f.StartsAt = now.Add(-time.Hour) }, "startsAt"},
		{"empieza en 8 días", func(f *AfterFields, _ *AfterValidation) { f.StartsAt = now.Add(8 * 24 * time.Hour) }, "startsAt"},
		{"termina antes de empezar", func(f *AfterFields, _ *AfterValidation) { f.EndsAt = ptr(f.StartsAt.Add(-time.Minute)) }, "endsAt"},
		{"dura más de 24 h", func(f *AfterFields, _ *AfterValidation) { f.EndsAt = ptr(f.StartsAt.Add(25 * time.Hour)) }, "endsAt"},
		{"portada ajena", func(f *AfterFields, _ *AfterValidation) { f.CoverKey = ptr("afters/otro-usuario/1.jpg") }, "coverKey"},
		{"portada con ..", func(f *AfterFields, _ *AfterValidation) { f.CoverKey = ptr("afters/" + host + "/../x.jpg") }, "coverKey"},
		{"antes del evento", func(f *AfterFields, v *AfterValidation) {
			f.EventID, v.Event, f.StartsAt = ptr("ev"), event, now.Add(30*time.Minute)
		}, "start of the event"},
		{"evento inexistente", func(f *AfterFields, v *AfterValidation) { f.EventID, v.Event = ptr("ev"), nil }, "invalid_event"},
		{"evento terminado hace más de 12 h", func(f *AfterFields, v *AfterValidation) {
			f.EventID = ptr("ev")
			v.Event = &AfterEventWindow{StartsAt: now.Add(-30 * time.Hour), EndsAt: now.Add(-13 * time.Hour)}
		}, "invalid_event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, v := base(), check
			tc.mutate(&fields, &v)
			_, err := NormalizeAfter(fields, v)
			if tc.want == "invalid_event" {
				if !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("want ErrInvalidEvent, got %v", err)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || !strings.Contains(validation.Message, tc.want) {
				t.Fatalf("want validation error about %q, got %v", tc.want, err)
			}
		})
	}

	// Free/approval descartan precio y link; el evento recién terminado (menos de 12 h) todavía vale.
	fields := base()
	fields.Price, fields.TicketURL, fields.EventID = ptr("$500"), ptr("https://x.example"), ptr("ev")
	v := check
	v.Event = &AfterEventWindow{StartsAt: now.Add(-6 * time.Hour), EndsAt: now.Add(-time.Hour)}
	got, err := NormalizeAfter(fields, v)
	if err != nil || got.Price != nil || got.TicketURL != nil {
		t.Fatalf("free after drops price and link: %+v %v", got, err)
	}
	// En un PATCH que no cambia startsAt no se revalida la ventana (el after ya empezó).
	started := base()
	started.StartsAt = now.Add(-3 * time.Hour)
	if _, err := NormalizeAfter(started, AfterValidation{Now: now, HostID: host}); err != nil {
		t.Fatalf("an unchanged startsAt is not revalidated: %v", err)
	}
	cover := base()
	cover.CoverKey = ptr("afters/" + host + "/123.jpg")
	if _, err := NormalizeAfter(cover, check); err != nil {
		t.Fatalf("own cover key: %v", err)
	}
}

func TestAfterPatchApply(t *testing.T) {
	current := AfterFields{Name: "A", Zone: "Z", Capacity: ptr(10), Lineup: ptr("DJ")}
	next := AfterPatch{Name: ptr("B"), Capacity: Nullable[int]{Set: true}, Lineup: Nullable[string]{}}.Apply(current)
	if next.Name != "B" || next.Capacity != nil || next.Lineup == nil || next.Zone != "Z" {
		t.Fatalf("patch: %+v", next)
	}
}

func TestNormalizeSubmission(t *testing.T) {
	now := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	user := "22222222-2222-2222-2222-222222222222"
	valid := EventSubmissionInput{Name: "Fiesta", StartsAt: now.Add(48 * time.Hour), VenueName: "Club", VenueAddress: "Calle 1",
		FlyerKey: "event-submissions/" + user + "/1.jpg", SubmitterRole: "Organizer", Contact: "@club", RightsConfirmed: true}
	if got, err := NormalizeSubmission(valid, user, now); err != nil || got.SubmitterRole != SubmitterOrganizer {
		t.Fatalf("valid submission: %+v %v", got, err)
	}
	for name, mutate := range map[string]func(*EventSubmissionInput){
		"sin derechos":   func(in *EventSubmissionInput) { in.RightsConfirmed = false },
		"flyer ajeno":    func(in *EventSubmissionInput) { in.FlyerKey = "event-submissions/otro/1.jpg" },
		"rol inválido":   func(in *EventSubmissionInput) { in.SubmitterRole = "dj" },
		"pasado":         func(in *EventSubmissionInput) { in.StartsAt = now.Add(-48 * time.Hour) },
		"ticket http":    func(in *EventSubmissionInput) { in.TicketURL = ptr("http://x.example") },
		"sin contacto":   func(in *EventSubmissionInput) { in.Contact = " " },
		"sin dirección":  func(in *EventSubmissionInput) { in.VenueAddress = "" },
		"nombre gigante": func(in *EventSubmissionInput) { in.Name = strings.Repeat("x", 121) },
	} {
		in := valid
		mutate(&in)
		var validation *ValidationError
		if _, err := NormalizeSubmission(in, user, now); !errors.As(err, &validation) {
			t.Fatalf("%s: want a validation error, got %v", name, err)
		}
	}
}
