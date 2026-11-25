package postgres

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"root-backend-service/internal/core/domain"
)

func TestApplyCrewChatFields(t *testing.T) {
	date := sql.NullTime{Time: time.Date(2026, 12, 5, 23, 0, 0, 0, time.UTC), Valid: true}

	temporary := domain.Chat{EventTitle: "Fiesta", EventLocation: "Montevideo", EventImage: "flyer.jpg"}
	applyCrewChatFields(&temporary, "temporary", date)
	if temporary.CrewType != "temporary" || temporary.EventTitle != "Fiesta" || temporary.EventDate == nil || temporary.EventImage != "flyer.jpg" {
		t.Fatalf("temporary chats carry the event: %+v", temporary)
	}

	// Permanentes y squads del matcher no reciben datos del evento ni cambian su respuesta anterior.
	permanent := domain.Chat{EventTitle: "Fiesta", EventLocation: "Montevideo", EventImage: "flyer.jpg"}
	applyCrewChatFields(&permanent, "permanent", date)
	if permanent.CrewType != "permanent" || permanent.EventTitle != "" || permanent.EventDate != nil || permanent.EventImage != "" {
		t.Fatalf("permanent chats have no event data: %+v", permanent)
	}
	matcher := domain.Chat{EventTitle: "x"}
	applyCrewChatFields(&matcher, "event_match", date)
	direct := domain.Chat{}
	applyCrewChatFields(&direct, "", sql.NullTime{})
	for _, chat := range []domain.Chat{matcher, direct} {
		raw, _ := json.Marshal(chat)
		for _, key := range []string{"crew_type", "event_title", "event_date", "event_location", "event_image"} {
			if strings.Contains(string(raw), key) {
				t.Errorf("%s must be omitted for non-crew chats: %s", key, raw)
			}
		}
	}
}
