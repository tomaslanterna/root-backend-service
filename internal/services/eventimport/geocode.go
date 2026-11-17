package eventimport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Geocoder resolves a venue to coordinates.
type Geocoder interface {
	Geocode(ctx context.Context, venue, address, city string) (*Place, error)
}

// Nominatim geocodes with OpenStreetMap. Its usage policy allows one request
// per second and requires an identifying User-Agent, so calls are spaced and
// answers cached for the whole run.
type Nominatim struct {
	BaseURL string
	Client  *http.Client
	mu      sync.Mutex
	last    time.Time
	cache   map[string]*Place
}

func NewNominatim() *Nominatim {
	return &Nominatim{
		BaseURL: "https://nominatim.openstreetmap.org",
		Client:  &http.Client{Timeout: 15 * time.Second},
		cache:   map[string]*Place{},
	}
}

type nominatimResult struct {
	Lat     string `json:"lat"`
	Lon     string `json:"lon"`
	Address struct {
		State        string `json:"state"`
		Region       string `json:"region"`
		City         string `json:"city"`
		Town         string `json:"town"`
		Village      string `json:"village"`
		Municipality string `json:"municipality"`
		Suburb       string `json:"suburb"`
	} `json:"address"`
}

func (n *Nominatim) Geocode(ctx context.Context, venue, address, city string) (*Place, error) {
	var queries []string
	if address != "" {
		queries = append(queries, join(address, city, "Uruguay"))
	}
	queries = append(queries, join(venue, city, "Uruguay"), join(venue, "Uruguay"))
	for _, q := range queries {
		place, err := n.lookup(ctx, q)
		if err != nil {
			return nil, err
		}
		if place != nil {
			return place, nil
		}
	}
	return nil, nil
}

func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, ", ")
}

func (n *Nominatim) lookup(ctx context.Context, query string) (*Place, error) {
	n.mu.Lock()
	if place, ok := n.cache[query]; ok {
		n.mu.Unlock()
		return place, nil
	}
	if wait := time.Second + 100*time.Millisecond - time.Since(n.last); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			n.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	n.last = time.Now()
	n.mu.Unlock()

	params := url.Values{"q": {query}, "format": {"jsonv2"}, "addressdetails": {"1"}, "countrycodes": {"uy"}, "limit": {"1"}, "accept-language": {"es"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.BaseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := n.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim respondió %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	place := parseNominatim(body)
	n.mu.Lock()
	n.cache[query] = place
	n.mu.Unlock()
	return place, nil
}

// parseNominatim returns the first result if it lies inside Uruguay.
func parseNominatim(body []byte) *Place {
	var results []nominatimResult
	if json.Unmarshal(body, &results) != nil || len(results) == 0 {
		return nil
	}
	r := results[0]
	lat, err1 := strconv.ParseFloat(r.Lat, 64)
	lng, err2 := strconv.ParseFloat(r.Lon, 64)
	if err1 != nil || err2 != nil || !InUruguay(lat, lng) {
		return nil
	}
	a := r.Address
	dept := strings.TrimSpace(strings.TrimPrefix(firstNonEmpty(a.State, a.Region), "Departamento de "))
	return &Place{
		Lat: lat, Lng: lng, Department: dept,
		City: firstNonEmpty(a.City, a.Town, a.Village, a.Municipality, a.Suburb),
	}
}

// InUruguay is a rough sanity check, not a border: the country's bounding box
// minus the Río de la Plata corner, where the box covers Buenos Aires.
func InUruguay(lat, lng float64) bool {
	if lat < -35.1 || lat > -30.0 || lng < -58.5 || lng > -53.0 {
		return false
	}
	return !(lat < -34.2 && lng < -57.9)
}
