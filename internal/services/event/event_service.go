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

func (s *EventService) GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error) {
	return s.eventRepo.GetEvents(ctx, filter, currentUserID)
}

func (s *EventService) GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error) {
	return s.eventRepo.GetEventByID(ctx, id, currentUserID)
}

func (s *EventService) RSVPEvent(ctx context.Context, userID, eventID, status string) (int, int, string, error) {
	return s.eventRepo.RSVPEvent(ctx, userID, eventID, status)
}

func (s *EventService) GetFollowedGoingAttendees(ctx context.Context, eventID string, currentUserID string, limit, offset int) ([]domain.Attendee, int, error) {
	return s.eventRepo.GetFollowedGoingAttendees(ctx, eventID, currentUserID, limit, offset)
}

func (s *EventService) GetEventComments(ctx context.Context, eventID string, limit, offset int) ([]domain.EventComment, int, error) {
	return s.eventRepo.GetEventComments(ctx, eventID, limit, offset)
}

func (s *EventService) CreateEventComment(ctx context.Context, eventID string, authorID string, content string) (*domain.EventComment, error) {
	return s.eventRepo.CreateEventComment(ctx, eventID, authorID, content)
}
