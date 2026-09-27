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
	// Validate event exists and get its coordinates
	event, err := s.eventRepo.GetEventByID(ctx, req.EventID, userID)
	if err != nil {
		return nil, errors.New("event not found")
	}

	isValidated := false

	// Validate location using Haversine
	if event.Latitude != nil && event.Longitude != nil {
		radius := 200 // default
		if event.GeofenceRadiusMeters != nil {
			radius = *event.GeofenceRadiusMeters
		}

		dist := haversine(req.Lat, req.Lng, *event.Latitude, *event.Longitude)
		if dist <= float64(radius) {
			isValidated = true
		}
	} else {
		// If event has no location, we might trust the device for now or reject. 
		// Let's validate it to true for backward compatibility or mock events.
		isValidated = true
	}

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

func (s *danceService) GetCrewLeaderboards(ctx context.Context, squadID string, eventID *string) (map[string]interface{}, error) {
	allTime, err := s.danceRepo.GetCrewLeaderboardAllTime(ctx, squadID)
	if err != nil {
		return nil, err
	}

	response := map[string]interface{}{
		"allTime": allTime,
	}

	if eventID != nil && *eventID != "" {
		byEvent, err := s.danceRepo.GetCrewLeaderboardByEvent(ctx, squadID, *eventID)
		if err == nil {
			response["byEvent"] = byEvent
		}
	}

	return response, nil
}

func (s *danceService) GetUserCrews(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetUserCrews(ctx, userID)
}

func (s *danceService) GetUserDanceSessions(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetUserDanceSessions(ctx, userID)
}

func (s *danceService) GetCrewByID(ctx context.Context, squadID string) (map[string]interface{}, error) {
	crew, err := s.danceRepo.GetCrewByID(ctx, squadID)
	if err != nil {
		return nil, err
	}
	allTime, _ := s.danceRepo.GetCrewLeaderboardAllTime(ctx, squadID)
	crew["leaderboardAllTime"] = allTime
	return crew, nil
}

func (s *danceService) GetCrewEvents(ctx context.Context, squadID string) ([]map[string]interface{}, error) {
	return s.danceRepo.GetCrewEvents(ctx, squadID)
}
