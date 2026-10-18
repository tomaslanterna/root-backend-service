package services

import (
	"context"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type fakeDanceRepo struct {
	savedSession *domain.DanceSession
	allTime      []map[string]interface{}
	byEvent      map[string][]map[string]interface{}
	crewEvents   []map[string]interface{}
	createdCrew  map[string]interface{}
}

func (f *fakeDanceRepo) InitSchema(ctx context.Context) error { return nil }

func (f *fakeDanceRepo) SaveDanceSession(ctx context.Context, session *domain.DanceSession) error {
	f.savedSession = session
	return nil
}

func (f *fakeDanceRepo) CreatePermanentCrew(ctx context.Context, creatorID string, req ports.CreateCrewRequest) (map[string]interface{}, error) {
	f.createdCrew = map[string]interface{}{
		"id":         "crew-1",
		"name":       req.Name,
		"creatorId":  creatorID,
		"member_ids": req.MemberIDs,
		"event_ids":  req.EventIDs,
	}
	return f.createdCrew, nil
}

func (f *fakeDanceRepo) JoinPermanentCrew(ctx context.Context, userID, codeOrID string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": "crew-1", "joinedUser": userID, "code": codeOrID}, nil
}

func (f *fakeDanceRepo) AddCrewEvent(ctx context.Context, squadID, eventID, userID string) error {
	return nil
}

func (f *fakeDanceRepo) GetCrewLeaderboardAllTime(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	return f.allTime, nil
}

func (f *fakeDanceRepo) GetCrewLeaderboardByEvent(ctx context.Context, squadID, eventID string) ([]map[string]interface{}, error) {
	return f.byEvent[eventID], nil
}

func (f *fakeDanceRepo) GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return nil, nil
}

func (f *fakeDanceRepo) GetAllCrews(ctx context.Context) ([]map[string]interface{}, error) {
	return nil, nil
}

func (f *fakeDanceRepo) GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return nil, nil
}

func (f *fakeDanceRepo) GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": squadID}, nil
}

func (f *fakeDanceRepo) GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	return f.crewEvents, nil
}

type fakeEventRepoForDance struct {
	events map[string]*domain.Event
}

func (f *fakeEventRepoForDance) GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error) {
	ev, ok := f.events[id]
	if !ok {
		return nil, domain.ErrSquadNotFound
	}
	return ev, nil
}
func (f *fakeEventRepoForDance) GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error) {
	return nil, nil
}
func (f *fakeEventRepoForDance) GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error) {
	return nil, 0, nil
}
func (f *fakeEventRepoForDance) RSVPEvent(ctx context.Context, userID, eventID, status string) (int, int, string, error) {
	return 0, 0, "", nil
}
func (f *fakeEventRepoForDance) SearchEvents(ctx context.Context, query string) ([]domain.Event, error) {
	return nil, nil
}
func (f *fakeEventRepoForDance) GetUserEvents(ctx context.Context, username string) ([]domain.Event, error) {
	return nil, nil
}
func (f *fakeEventRepoForDance) GetFollowedGoingAttendees(ctx context.Context, eventID string, currentUserID string, limit, offset int) ([]domain.Attendee, int, error) {
	return nil, 0, nil
}
func (f *fakeEventRepoForDance) GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error) {
	return nil, 0, nil
}
func (f *fakeEventRepoForDance) CreateEventComment(ctx context.Context, eventID string, authorID string, content string) (*domain.EventComment, error) {
	return nil, nil
}
func (f *fakeEventRepoForDance) GetPendingSurveys(ctx context.Context, userID string) ([]domain.Event, error) {
	return nil, nil
}
func (f *fakeEventRepoForDance) UpdateEventTicketInfo(ctx context.Context, id string, tiers []domain.TicketTier, source *string) error {
	return nil
}
func (f *fakeEventRepoForDance) GetLiveEventStatus(ctx context.Context, userID string, lat, lng float64) (*domain.Event, int, error) {
	return nil, 0, nil
}
func (f *fakeEventRepoForDance) BulkCreateEvents(ctx context.Context, events []domain.Event) error {
	return nil
}
func (f *fakeEventRepoForDance) InitSchema(ctx context.Context) error { return nil }

func TestSyncStepsUsesDatabaseGeofenceRadius(t *testing.T) {
	// Event 1: Key presenta Maceo Plex (1000m radius at -34.86462740, -56.01289570)
	latMaceo := -34.86462740
	lngMaceo := -56.01289570
	radius1000 := 1000

	// Event 2: Prueba fiesta Tomi (500m radius at -34.90814570, -56.18422980)
	latTomi := -34.90814570
	lngTomi := -56.18422980
	radius500 := 500

	// Event 3: Event with NULL radius in DB (should fallback to 300m)
	eventRepo := &fakeEventRepoForDance{
		events: map[string]*domain.Event{
			"aaeb5120-8a38-41be-9734-eddd9f5dd404": {
				ID:                   "aaeb5120-8a38-41be-9734-eddd9f5dd404",
				Title:                "Key presenta Maceo Plex",
				Latitude:             &latMaceo,
				Longitude:            &lngMaceo,
				GeofenceRadiusMeters: &radius1000,
			},
			"7b1a602d-0051-46a4-90f1-216768ef0247": {
				ID:                   "7b1a602d-0051-46a4-90f1-216768ef0247",
				Title:                "Prueba fiesta Tomi",
				Latitude:             &latTomi,
				Longitude:            &lngTomi,
				GeofenceRadiusMeters: &radius500,
			},
			"event-null-radius": {
				ID:                   "event-null-radius",
				Title:                "Evento sin radio explicito",
				Latitude:             &latTomi,
				Longitude:            &lngTomi,
				GeofenceRadiusMeters: nil,
			},
		},
	}
	danceRepo := &fakeDanceRepo{}
	svc := NewDanceService(danceRepo, eventRepo)

	// 1. At ~650m from Maceo Plex (0.0058 deg lat is ~645m):
	// Should be VALIDATED because event has geofence_radius_meters = 1000
	session, err := svc.SyncSteps(context.Background(), "user-1", ports.SyncDanceRequest{
		EventID:    "aaeb5120-8a38-41be-9734-eddd9f5dd404",
		StepsCount: 250,
		StartTime:  time.Now().Add(-time.Minute),
		EndTime:    time.Now(),
		Lat:        latMaceo + 0.0058,
		Lng:        lngMaceo,
	})
	if err != nil {
		t.Fatalf("unexpected error syncing steps: %v", err)
	}
	if !session.IsValidated {
		t.Fatalf("expected session at ~645m to be validated for 1000m radius event, got IsValidated=false")
	}

	// 2. At ~1300m from Maceo Plex (0.012 deg lat is ~1334m):
	// Should NOT be validated because 1334m > 1000m
	session, err = svc.SyncSteps(context.Background(), "user-1", ports.SyncDanceRequest{
		EventID:    "aaeb5120-8a38-41be-9734-eddd9f5dd404",
		StepsCount: 250,
		StartTime:  time.Now().Add(-time.Minute),
		EndTime:    time.Now(),
		Lat:        latMaceo + 0.012,
		Lng:        lngMaceo,
	})
	if err != nil {
		t.Fatalf("unexpected error syncing steps: %v", err)
	}
	if session.IsValidated {
		t.Fatalf("expected session at ~1334m to NOT be validated for 1000m radius event, got IsValidated=true")
	}

	// 3. At ~400m from Prueba fiesta Tomi (0.0036 deg lat is ~400m):
	// Should be VALIDATED because event has geofence_radius_meters = 500
	session, err = svc.SyncSteps(context.Background(), "user-1", ports.SyncDanceRequest{
		EventID:    "7b1a602d-0051-46a4-90f1-216768ef0247",
		StepsCount: 120,
		StartTime:  time.Now().Add(-time.Minute),
		EndTime:    time.Now(),
		Lat:        latTomi + 0.0036,
		Lng:        lngTomi,
	})
	if err != nil {
		t.Fatalf("unexpected error syncing steps: %v", err)
	}
	if !session.IsValidated {
		t.Fatalf("expected session at ~400m to be validated for 500m radius event, got IsValidated=false")
	}

	// 4. At ~400m on event with NULL geofence_radius_meters (fallback 300m):
	// Should NOT be validated because 400m > 300m fallback
	session, err = svc.SyncSteps(context.Background(), "user-1", ports.SyncDanceRequest{
		EventID:    "event-null-radius",
		StepsCount: 120,
		StartTime:  time.Now().Add(-time.Minute),
		EndTime:    time.Now(),
		Lat:        latTomi + 0.0036,
		Lng:        lngTomi,
	})
	if err != nil {
		t.Fatalf("unexpected error syncing steps: %v", err)
	}
	if session.IsValidated {
		t.Fatalf("expected session at ~400m to NOT be validated when radius is NULL (300m fallback), got IsValidated=true")
	}
}

func TestGetCrewLeaderboardsResolvesLatestCrewEventWhenNoneProvided(t *testing.T) {
	danceRepo := &fakeDanceRepo{
		allTime: []map[string]interface{}{
			{"userId": "u1", "name": "Tomas", "steps": 1500},
			{"userId": "u2", "name": "Amigo", "steps": 0},
		},
		crewEvents: []map[string]interface{}{
			{"id": "aaeb5120-8a38-41be-9734-eddd9f5dd404", "title": "Key presenta Maceo Plex"},
		},
		byEvent: map[string][]map[string]interface{}{
			"aaeb5120-8a38-41be-9734-eddd9f5dd404": {
				{"userId": "u1", "name": "Tomas", "steps": 900},
				{"userId": "u2", "name": "Amigo", "steps": 0},
			},
		},
	}
	svc := NewDanceService(danceRepo, &fakeEventRepoForDance{})

	res, err := svc.GetCrewLeaderboards(context.Background(), "crew-1", nil)
	if err != nil {
		t.Fatalf("unexpected error getting crew leaderboards: %v", err)
	}
	if res["eventId"] != "aaeb5120-8a38-41be-9734-eddd9f5dd404" {
		t.Fatalf("expected default eventId to be aaeb5120-8a38-41be-9734-eddd9f5dd404, got %v", res["eventId"])
	}
	byEvent, ok := res["byEvent"].([]map[string]interface{})
	if !ok || len(byEvent) != 2 {
		t.Fatalf("expected 2 members in byEvent leaderboard, got %+v", res["byEvent"])
	}
}
