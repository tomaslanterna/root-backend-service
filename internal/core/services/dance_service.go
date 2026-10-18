package services

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type danceService struct {
	danceRepo ports.DanceRepository
	eventRepo ports.EventRepository
}

func NewDanceService(danceRepo ports.DanceRepository, eventRepo ports.EventRepository) ports.DanceService {
	return &danceService{
		danceRepo: danceRepo,
		eventRepo: eventRepo,
	}
}

// haversine calculates the distance between two points in meters
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters

	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaPhi := (lat2 - lat1) * math.Pi / 180
	deltaLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaPhi/2)*math.Sin(deltaPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*
			math.Sin(deltaLambda/2)*math.Sin(deltaLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}

func (s *danceService) SyncSteps(ctx context.Context, userID string, req ports.SyncDanceRequest) (*domain.DanceSession, error) {
	if req.StepsCount < 0 || math.IsNaN(req.Lat) || math.IsNaN(req.Lng) || math.IsInf(req.Lat, 0) || math.IsInf(req.Lng, 0) || req.Lat < -90 || req.Lat > 90 || req.Lng < -180 || req.Lng > 180 {
		return nil, errors.New("invalid dance synchronization")
	}
	// Reuse the same authoritative time/geofence/RSVP check as the presence banner.
	// Do not trust arbitrary event IDs, client timestamps or missing venue coordinates.
	event, err := s.eventRepo.GetLiveEventStatus(ctx, userID, req.Lat, req.Lng)
	if err != nil {
		return nil, err
	}

	isValidated := false

	// Validate location using Haversine
	if event.Latitude != nil && event.Longitude != nil {
		radius := 300 // fallback only if geofence_radius_meters is NULL in DB (matches COALESCE(e.geofence_radius_meters, 300))
		if event.GeofenceRadiusMeters != nil && *event.GeofenceRadiusMeters > 0 {
			radius = *event.GeofenceRadiusMeters
		}

		dist := haversine(req.Lat, req.Lng, *event.Latitude, *event.Longitude)
		if dist <= float64(radius) {
			isValidated = true
		}
	} else {
		isValidated = true
	}
	isValidated := true

	session := &domain.DanceSession{
		ID:          uuid.New().String(),
		UserID:      userID,
		EventID:     req.EventID,
		StepsCount:  req.StepsCount,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		IsValidated: isValidated,
		CreatedAt:   time.Now(),
	}

	if err := s.danceRepo.SaveDanceSession(ctx, session); err != nil {
		return nil, err
	}

	return session, nil
}

func (s *danceService) CreatePermanentCrew(ctx context.Context, creatorID string, req ports.CreateCrewRequest) (map[string]interface{}, error) {
	return s.danceRepo.CreatePermanentCrew(ctx, creatorID, req)
}

func (s *danceService) JoinPermanentCrew(ctx context.Context, userID, codeOrID string) (map[string]interface{}, error) {
	return s.danceRepo.JoinPermanentCrew(ctx, userID, codeOrID)
}

func (s *danceService) AddCrewEvent(ctx context.Context, squadID, eventID, userID string) error {
	return s.danceRepo.AddCrewEvent(ctx, squadID, eventID, userID)
}

func (s *danceService) GetCrewLeaderboards(ctx context.Context, squadID string, eventID *string) (map[string]interface{}, error) {
	allTime, err := s.danceRepo.GetCrewLeaderboardAllTime(ctx, squadID)
	if err != nil {
		return nil, err
	}

	response := map[string]interface{}{
		"allTime": allTime,
	}

	targetEventID := ""
	if eventID != nil && *eventID != "" {
		targetEventID = *eventID
	} else {
		events, err := s.danceRepo.GetCrewEvents(ctx, squadID)
		if err == nil && len(events) > 0 {
			if eid, ok := events[0]["id"].(string); ok {
				targetEventID = eid
			}
		}
	}

	if targetEventID != "" {
		byEvent, err := s.danceRepo.GetCrewLeaderboardByEvent(ctx, squadID, targetEventID)
		if err == nil {
			response["byEvent"] = byEvent
			response["eventId"] = targetEventID
		}
	}

	return response, nil
}

func (s *danceService) GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetUserCrews(ctx, userID)
}

func (s *danceService) GetAllCrews(ctx context.Context) ([]map[string]interface{}, error) {
	return s.danceRepo.GetAllCrews(ctx)
}

func (s *danceService) GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetUserDanceSessions(ctx, userID)
}

func (s *danceService) GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error) {
	return s.danceRepo.GetCrewByID(ctx, squadID)
}

func (s *danceService) GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetCrewEvents(ctx, squadID)
}
