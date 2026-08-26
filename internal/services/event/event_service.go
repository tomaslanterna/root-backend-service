package event

import (
	"context"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type EventService struct {
	eventRepo ports.EventRepository
}

func NewEventService(eventRepo ports.EventRepository) ports.EventService {
	return &EventService{
		eventRepo: eventRepo,
	}
}

func (s *EventService) GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error) {
	return s.eventRepo.GetFeaturedEvents(ctx, country)
}

func (s *EventService) GetEvents(ctx context.Context, featuredOnly *bool, country string) ([]domain.Event, error) {
	return s.eventRepo.GetEvents(ctx, featuredOnly, country)
}

func (s *EventService) GetEventByID(ctx context.Context, id string) (*domain.Event, error) {
	return s.eventRepo.GetEventByID(ctx, id)
}

func (s *EventService) RSVPEvent(ctx context.Context, userID, eventID, status string) (int, int, error) {
	return s.eventRepo.RSVPEvent(ctx, userID, eventID, status)
}
