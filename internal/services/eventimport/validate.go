package eventimport

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Montevideo is the zone every event time is interpreted in.
var Montevideo = mustLoad("America/Montevideo")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("UYT", -3*3600)
	}
	return loc
}

var startLayouts = []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

// parseLocal reads a time in Uruguay local time. Values with an explicit
// offset are converted to it. The result is the wall-clock time in UTC form,
// which is how events.date is stored.
func parseLocal(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		t = t.In(Montevideo)
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC), nil
	}
	for _, layout := range startLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("fecha inválida %q", raw)
}

// Validate turns an untrusted candidate into an Event or explains why not.
func Validate(c Candidate, w Window) (Event, error) {
	title := clean(c.Title, 140)
	venue := clean(c.VenueName, 120)
	if len([]rune(title)) < 3 {
		return Event{}, fmt.Errorf("título vacío")
	}
	if venue == "" {
		return Event{}, fmt.Errorf("sin venue")
	}
	start, err := parseLocal(c.StartsAt)
	if err != nil {
		return Event{}, err
	}
	if start.Before(w.From) || start.After(w.To) {
		return Event{}, fmt.Errorf("fecha %s fuera de la ventana", start.Format("2006-01-02"))
	}
	ev := Event{
		Title:       title,
		StartsAt:    start,
		VenueName:   venue,
		Address:     clean(c.Address, 200),
		City:        clean(c.City, 80),
		Genre:       clean(c.Genre, 100),
		Description: clean(c.Description, 1200),
		EventURL:    cleanURL(c.EventURL),
		TicketURL:   cleanURL(c.TicketURL),
		Lineup:      cleanLineup(c.Lineup, title),
	}
	if c.EndsAt != "" {
		if end, err := parseLocal(c.EndsAt); err == nil && end.After(start) && end.Sub(start) <= 48*time.Hour {
			ev.EndsAt = &end
		}
	}
	return ev, nil
}

func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// cleanURL keeps only plain http(s) URLs, without fragments or tracking params.
func cleanURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	q := u.Query()
	for key := range q {
		if isTrackingParam(strings.ToLower(key)) {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

var notArtists = map[string]bool{"desconocido": true, "unknown": true, "tba": true, "a confirmar": true, "n/a": true}

func cleanLineup(names []string, title string) []string {
	seen := map[string]bool{normalize(title): true}
	var out []string
	for _, name := range names {
		name = clean(name, 80)
		key := normalize(name)
		if key == "" || seen[key] || notArtists[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
		if len(out) == 30 {
			break
		}
	}
	return out
}

// isTrackingParam covers analytics and promoter/referral codes: a stored link
// must not credit somebody else's tracking.
func isTrackingParam(key string) bool {
	if strings.HasPrefix(key, "utm_") {
		return true
	}
	switch key {
	case "fbclid", "igshid", "gclid", "mc_cid", "mc_eid", "rrpp", "ref", "referrer", "aff", "affiliate", "promoter":
		return true
	}
	return false
}
