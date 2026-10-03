package event

import (
	"context"
	"log"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type EventService struct {
	eventRepo       ports.EventRepository
	artistRepo      ports.ArtistRepository
	aiTicketService ports.AiTicketService
}

func NewEventService(eventRepo ports.EventRepository, artistRepo ports.ArtistRepository, aiTicketService ports.AiTicketService) ports.EventService {
	return &EventService{
		eventRepo:       eventRepo,
		artistRepo:      artistRepo,
		aiTicketService: aiTicketService,
	}
}

func (s *EventService) GetFeaturedEvents(ctx context.Context, country string) ([]domain.Event, error) {
	return s.eventRepo.GetFeaturedEvents(ctx, country)
}

func (s *EventService) GetEvents(ctx context.Context, filter domain.EventFilter, currentUserID string) ([]domain.Event, int, error) {
	return s.eventRepo.GetEvents(ctx, filter, currentUserID)
}

func (s *EventService) GetEventByID(ctx context.Context, id string, currentUserID string) (*domain.Event, error) {
	event, err := s.eventRepo.GetEventByID(ctx, id, currentUserID)
	if err != nil {
		return nil, err
	}
	
	// Fetch lineup
	lineup, err := s.artistRepo.GetEventLineup(ctx, id)
	if err == nil && len(lineup) > 0 {
		event.Artists = lineup
	}

	// Ticket TTL Cache Logic
	now := time.Now()
	needsUpdate := event.TicketInfoFetched == nil || now.Sub(*event.TicketInfoFetched) > time.Hour

	if needsUpdate && event.TicketURL != nil && *event.TicketURL != "" {
		ticketData, err := s.aiTicketService.FetchEventTickets(ctx, event.Title, event.Date.Format("2006-01-02"), event.Location, "", *event.TicketURL)
		if err == nil && ticketData != nil {
			err = s.eventRepo.UpdateEventTicketInfo(ctx, event.ID, ticketData.TicketTiers, ticketData.Source)
			if err != nil {
				log.Printf("Error updating ticket info in DB: %v", err)
			} else {
				event.TicketTiers = ticketData.TicketTiers
				event.TicketSource = ticketData.Source
				event.TicketInfoFetched = &now
			}
		} else {
			log.Printf("Error fetching tickets from Gemini: %v", err)
		}
	}
	
	return event, nil
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

func (s *EventService) GetPendingSurveys(ctx context.Context, userID string) ([]domain.Event, error) {
	return s.eventRepo.GetPendingSurveys(ctx, userID)
}

func (s *EventService) GetUserEvents(ctx context.Context, username string) ([]domain.Event, error) {
	return s.eventRepo.GetUserEvents(ctx, username)
}

func (s *EventService) GetLiveEventStatus(ctx context.Context, userID string, lat, lng float64) (*domain.Event, error) {
	return s.eventRepo.GetLiveEventStatus(ctx, userID, lat, lng)
}
