package handlers

import (
	"net/http"
	"sort"
	"time"
)

type EventHandler struct{}

func NewEventHandler() *EventHandler {
	return &EventHandler{}
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":                 "e1",
				"title":              "AFTERLIFE BUENOS AIRES",
				"producerId":         "p1",
				"date":               "2024-03-08",
				"location":           "Mandarine Park",
				"cinematicBannerUrl": "https://...",
				"description":        "Una odisea visual...",
				"lineup":             []string{"Tale Of Us", "Anyma"},
				"goingCount":         184,
				"notGoingCount":      46,
			},
		},
		"meta": map[string]interface{}{
			"total": 15,
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) GetEventByID(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"id":                 "e1",
		"title":              "AFTERLIFE BUENOS AIRES",
		"producerId":         "p1",
		"date":               "2024-03-08",
		"location":           "Mandarine Park",
		"cinematicBannerUrl": "https://...",
		"description":        "Una odisea visual...",
		"lineup":             []string{"Tale Of Us", "Anyma"},
		"goingCount":         184,
		"notGoingCount":      46,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) RSVPEvent(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"success":       true,
		"goingCount":    185,
		"notGoingCount": 46,
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) GetEventTickets(w http.ResponseWriter, r *http.Request) {
	mockResponse := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"id":       "t1",
				"eventId":  "e1",
				"sellerId": "2",
				"price":    45000,
				"status":   "AVAILABLE",
			},
		},
	}
	respondWithJSON(w, http.StatusOK, mockResponse)
}

func (h *EventHandler) GetFeaturedEvents(w http.ResponseWriter, r *http.Request) {
	country := r.URL.Query().Get("country")
	if country == "" {
		country = "Uruguay" // Default
	}

	mockEvents := []map[string]interface{}{
		{
			"id":                 "e1",
			"title":              "AFTERLIFE BUENOS AIRES",
			"producerId":         "p1",
			"date":               "2024-03-08",
			"location":           "Mandarine Park",
			"country":            "Argentina",
			"cinematicBannerUrl": "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?q=80&w=2070&auto=format&fit=crop",
			"description":        "Una odisea visual y sonora única en el ámbito de la conciencia. Afterlife regresa a Buenos Aires...",
			"lineup":             []string{"Tale Of Us", "Anyma"},
			"goingCount":         184,
			"notGoingCount":      46,
		},
		{
			"id":                 "e2",
			"title":              "TIME WARP 2024",
			"producerId":         "p2",
			"date":               "2024-04-05",
			"location":           "Costa Salguero",
			"country":            "Argentina",
			"cinematicBannerUrl": "https://images.unsplash.com/photo-1514525253344-93168e974686?q=80&w=1974&auto=format&fit=crop",
			"description":        "La experiencia absoluta del techno alemán en dos noches inolvidables.",
			"lineup":             []string{"Sven Väth", "Nina Kraviz"},
			"goingCount":         152,
			"notGoingCount":      28,
		},
		{
			"id":                 "e5",
			"title":              "AFTERLIFE MONTEVIDEO",
			"producerId":         "p1",
			"date":               "2024-11-15",
			"location":           "Velódromo Municipal",
			"country":            "Uruguay",
			"cinematicBannerUrl": "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?q=80&w=2070&auto=format&fit=crop",
			"description":        "Llega a Montevideo el show visual más impactante del techno.",
			"lineup":             []string{"Tale Of Us", "Kevin de Vries"},
			"goingCount":         320,
			"notGoingCount":      12,
		},
		{
			"id":                 "e6",
			"title":              "ZAMNA PUNTA DEL ESTE",
			"producerId":         "p3",
			"date":               "2024-12-28",
			"location":           "Parada 11 Brava",
			"country":            "Uruguay",
			"cinematicBannerUrl": "https://images.unsplash.com/photo-1516450360452-9312f5e86fc7?q=80&w=1974&auto=format&fit=crop",
			"description":        "La magia de Tulum en Punta del Este.",
			"lineup":             []string{"Black Coffee", "Keinemusik"},
			"goingCount":         450,
			"notGoingCount":      30,
		},
	}

	// Filtrar por pais
	var filtered []map[string]interface{}
	for _, e := range mockEvents {
		if e["country"] == country {
			filtered = append(filtered, e)
		}
	}

	// Ordenar por proximidad a fecha actual (futuro más cercano primero)
	now := time.Now()
	sort.Slice(filtered, func(i, j int) bool {
		dateI, _ := time.Parse("2006-01-02", filtered[i]["date"].(string))
		dateJ, _ := time.Parse("2006-01-02", filtered[j]["date"].(string))
		
		diffI := dateI.Sub(now)
		diffJ := dateJ.Sub(now)
		
		if diffI >= 0 && diffJ >= 0 {
			return diffI < diffJ
		}
		if diffI >= 0 {
			return true
		}
		if diffJ >= 0 {
			return false
		}
		
		return diffI > diffJ 
	})

	mockResponse := map[string]interface{}{
		"data": filtered,
		"meta": map[string]interface{}{
			"total": len(filtered),
		},
	}

	respondWithJSON(w, http.StatusOK, mockResponse)
}
