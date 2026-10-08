package event

import (
	"context"
	"encoding/csv"
	"io"
	"log"
	"strconv"
	"strings"
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
		// Lanzamos la búsqueda de IA en background para no bloquear la respuesta
		go func(eID, title, dateStr, location, tURL string) {
			bgCtx := context.Background()
			ticketData, err := s.aiTicketService.FetchEventTickets(bgCtx, title, dateStr, location, "", tURL)
			if err == nil && ticketData != nil {
				err = s.eventRepo.UpdateEventTicketInfo(bgCtx, eID, ticketData.TicketTiers, ticketData.Source)
				if err != nil {
					log.Printf("Error updating ticket info in DB: %v", err)
				}
			} else {
				log.Printf("Error fetching tickets from Gemini: %v", err)
			}
		}(event.ID, event.Title, event.Date.Format("2006-01-02"), event.Location, *event.TicketURL)
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

func (s *EventService) GetLiveEventStatus(ctx context.Context, userID string, lat, lng float64) (*domain.Event, int, error) {
	return s.eventRepo.GetLiveEventStatus(ctx, userID, lat, lng)
}

func (s *EventService) BulkCreateFromCSV(ctx context.Context, file io.Reader) (int, error) {
	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	
	// Leer cabeceras y omitir
	_, err := reader.Read()
	if err != nil {
		return 0, err
	}

	records, err := reader.ReadAll()
	if err != nil {
		return 0, err
	}

	var events []domain.Event
	for _, record := range records {
		if len(record) < 5 {
			continue // Omitir si faltan los campos básicos
		}

		// Fecha_Hora;Nombre;Nombre del lugar;Latitud;Longitud;descripcion;bannerImage
		dateStr := strings.TrimSpace(record[0])
		title := strings.TrimSpace(record[1])
		location := strings.TrimSpace(record[2])
		latStr := strings.TrimSpace(record[3])
		lngStr := strings.TrimSpace(record[4])
		
		desc := ""
		banner := ""
		if len(record) > 5 {
			desc = strings.TrimSpace(record[5])
		}
		if len(record) > 6 {
			banner = strings.TrimSpace(record[6])
		}

		// Remover posibles comillas dobles de la exportacion
		desc = strings.Trim(desc, "\"")
		banner = strings.Trim(banner, "\"")

		parsedDate, err := time.Parse("2006-01-02 15:04:05", dateStr)
		if err != nil {
			log.Printf("Invalid date %s for event %s, skipping", dateStr, title)
			continue
		}

		var lat, lng *float64
		if latStr != "" && latStr != "-" {
			latStr = strings.ReplaceAll(latStr, ",", ".")
			if parsedLat, err := strconv.ParseFloat(latStr, 64); err == nil {
				lat = &parsedLat
			}
		}

		if lngStr != "" && lngStr != "-" {
			lngStr = strings.ReplaceAll(lngStr, ",", ".")
			if parsedLng, err := strconv.ParseFloat(lngStr, 64); err == nil {
				lng = &parsedLng
			}
		}

		event := domain.Event{
			Title:              title,
			Date:               parsedDate,
			Location:           location,
			Latitude:           lat,
			Longitude:          lng,
			Description:        desc,
			CinematicBannerURL: banner,
			Lineup:             []string{}, // Lineup como array vacío según lo pedido
			IsFree:             false,
			IsFeatured:         false,
		}
		events = append(events, event)
	}

	err = s.eventRepo.BulkCreateEvents(ctx, events)
	return len(events), err
}
