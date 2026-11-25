package eventimport

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genai"
)

// Discoverer finds candidate events inside a date window.
type Discoverer interface {
	Discover(ctx context.Context, w Window) ([]Candidate, error)
}

// GeminiDiscoverer searches the web with Gemini's Google Search grounding
// (step 1) and turns the findings into structured candidates (step 2). The two
// calls are separate because search grounding can't be combined with a JSON
// response schema.
type GeminiDiscoverer struct {
	Client  *genai.Client
	Model   string
	Web     *Web
	Regions []string
}

var defaultRegions = []string{
	"Montevideo",
	"Punta del Este y Maldonado",
	"el resto de Uruguay (Canelones, Colonia, Rocha, Paysandú, Salto, Florida, etc.)",
}

func NewGeminiDiscoverer(ctx context.Context, apiKey, model string, web *Web) (*GeminiDiscoverer, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey, Backend: genai.BackendGeminiAPI})
	if err != nil {
		return nil, fmt.Errorf("creando cliente Gemini: %w", err)
	}
	return &GeminiDiscoverer{Client: client, Model: model, Web: web, Regions: defaultRegions}, nil
}

var spanishMonths = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// months lists each calendar month touched by the window.
func months(w Window) []string {
	var out []string
	for t := time.Date(w.From.Year(), w.From.Month(), 1, 0, 0, 0, 0, time.UTC); !t.After(w.To); t = t.AddDate(0, 1, 0) {
		out = append(out, fmt.Sprintf("%s de %d", spanishMonths[t.Month()-1], t.Year()))
	}
	return out
}

func (d *GeminiDiscoverer) Discover(ctx context.Context, w Window) ([]Candidate, error) {
	var all []Candidate
	failures, calls := 0, 0
	for _, month := range months(w) {
		for _, region := range d.Regions {
			calls++
			found, err := d.search(ctx, month, region, w)
			if err != nil {
				failures++
				log.Printf("búsqueda %q / %q falló: %v", month, region, err)
				continue
			}
			log.Printf("búsqueda %q / %q: %d candidatos", month, region, len(found))
			all = append(all, found...)
		}
	}
	if calls > 0 && failures == calls {
		return nil, fmt.Errorf("todas las búsquedas fallaron")
	}
	return all, nil
}

func (d *GeminiDiscoverer) search(ctx context.Context, month, region string, w Window) ([]Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	prompt := fmt.Sprintf(`Buscá en internet fiestas y eventos de música electrónica (techno, house, tech house, melodic, trance, drum and bass, etc.) en %s, Uruguay, durante %s.
Solo eventos entre el %s y el %s. Ignorá eventos pasados, cancelados o sin fecha confirmada.
Para cada evento informá: nombre, fecha y hora de inicio en hora de Uruguay, lugar (nombre del venue), dirección o barrio, ciudad, line-up de DJs/artistas, género, una descripción breve, y la URL real de la página del evento o de la venta de entradas (Tickantel, Redtickets, Passline, entraste, Eventbrite, el sitio del productor, etc.).
Escribí "desconocido" en lo que no encuentres. No inventes datos ni URLs: solo lo que hayas visto en los resultados.`,
		region, month, w.From.Format("2006-01-02"), w.To.Format("2006-01-02"))

	resp, err := d.Client.Models.GenerateContent(ctx, d.Model, genai.Text(prompt), &genai.GenerateContentConfig{
		Tools:       []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
		Temperature: genai.Ptr[float32](0.1),
	})
	if err != nil {
		return nil, fmt.Errorf("búsqueda con Gemini: %w", err)
	}
	findings := strings.TrimSpace(resp.Text())
	if findings == "" {
		return nil, nil
	}
	sources := d.sources(ctx, resp)
	found, err := d.structure(ctx, findings, sources, w)
	for i := range found {
		found[i].Sources = sources
	}
	return found, err
}

// sources lists the pages the search actually consulted, with real URLs.
func (d *GeminiDiscoverer) sources(ctx context.Context, resp *genai.GenerateContentResponse) []string {
	var out []string
	if len(resp.Candidates) == 0 || resp.Candidates[0].GroundingMetadata == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, chunk := range resp.Candidates[0].GroundingMetadata.GroundingChunks {
		if chunk.Web == nil || chunk.Web.URI == "" {
			continue
		}
		real, err := d.Web.ResolveRedirect(ctx, chunk.Web.URI)
		if err != nil {
			log.Printf("fuente no resuelta: %v", err)
			continue
		}
		if real == "" || seen[real] {
			continue
		}
		seen[real] = true
		out = append(out, real)
		if len(out) == 25 {
			break
		}
	}
	return out
}

func (d *GeminiDiscoverer) structure(ctx context.Context, findings string, sources []string, w Window) ([]Candidate, error) {
	str := func(desc string) *genai.Schema { return &genai.Schema{Type: genai.TypeString, Description: desc} }
	schema := &genai.Schema{Type: genai.TypeArray, Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title":       str("Nombre del evento"),
			"startsAt":    str("Inicio, hora local de Uruguay, formato 2006-01-02T15:04"),
			"endsAt":      str("Fin en el mismo formato, vacío si no se sabe"),
			"venueName":   str("Nombre del lugar"),
			"address":     str("Dirección o barrio, vacío si no se sabe"),
			"city":        str("Ciudad"),
			"genre":       str("Género principal, ej. Techno, House"),
			"lineup":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "DJs y artistas, uno por elemento"},
			"description": str("Descripción breve"),
			"eventUrl":    str("URL de la página del evento, vacío si no hay"),
			"ticketUrl":   str("URL de venta de entradas, vacío si no hay"),
		},
		Required: []string{"title", "startsAt", "venueName"},
	}}
	prompt := fmt.Sprintf(`Convertí estos hallazgos en una lista JSON de eventos de música electrónica en Uruguay entre %s y %s.
Reglas: no agregues eventos que no estén en el texto; usá solo URLs que aparezcan en el texto o en la lista de fuentes; si un dato es desconocido dejá el campo vacío; fechas en hora local de Uruguay.

HALLAZGOS:
%s

FUENTES CONSULTADAS:
%s`, w.From.Format("2006-01-02"), w.To.Format("2006-01-02"), findings, strings.Join(sources, "\n"))

	resp, err := d.Client.Models.GenerateContent(ctx, d.Model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		Temperature:      genai.Ptr[float32](0),
	})
	if err != nil {
		return nil, fmt.Errorf("estructurando con Gemini: %w", err)
	}
	var out []Candidate
	if err := json.Unmarshal([]byte(resp.Text()), &out); err != nil {
		return nil, fmt.Errorf("JSON inválido de Gemini: %w", err)
	}
	// Search results come as Google redirect links: swap them for the real page.
	cache := map[string]string{}
	for i := range out {
		out[i].EventURL = d.realURL(ctx, out[i].EventURL, cache)
		out[i].TicketURL = d.realURL(ctx, out[i].TicketURL, cache)
	}
	return out, nil
}

// realURL resolves Google grounding links and drops any it can't resolve, so
// a redirect link never gets treated as the event's page.
func (d *GeminiDiscoverer) realURL(ctx context.Context, raw string, cache map[string]string) string {
	if raw == "" {
		return ""
	}
	if v, ok := cache[raw]; ok {
		return v
	}
	real, err := d.Web.ResolveRedirect(ctx, raw)
	if err != nil {
		log.Printf("no se pudo resolver %s: %v", raw, err)
		real = ""
	}
	real = cleanURL(real)
	if strings.Contains(real, "vertexaisearch.cloud.google.com") {
		real = ""
	}
	cache[raw] = real
	return real
}

var urlInText = regexp.MustCompile(`https?://[^\s)\]>"'*]+`)

// FindPages searches for the ticket page or official page of one event and
// returns every real URL the search consulted or mentioned.
func (d *GeminiDiscoverer) FindPages(ctx context.Context, ev Event) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	prompt := fmt.Sprintf(`Buscá la página de venta de entradas (Passline, Tickantel, Redtickets, entraste, Eventbrite, Vamos Eventos, etc.) o la página oficial del evento "%s" en %s, Uruguay, el %s. Respondé con las URLs de esas páginas.`,
		ev.Title, ev.VenueName, ev.StartsAt.Format("2006-01-02"))
	resp, err := d.Client.Models.GenerateContent(ctx, d.Model, genai.Text(prompt), &genai.GenerateContentConfig{
		Tools:       []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
		Temperature: genai.Ptr[float32](0),
	})
	if err != nil {
		return nil, fmt.Errorf("búsqueda con Gemini: %w", err)
	}
	urls := d.sources(ctx, resp)
	cache := map[string]string{}
	for _, raw := range urlInText.FindAllString(resp.Text(), 10) {
		if real := d.realURL(ctx, strings.TrimRight(raw, ".,;"), cache); real != "" {
			urls = append(urls, real)
		}
	}
	return uniqueURLs(urls...), nil
}
