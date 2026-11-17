package eventimport

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Pages is the web access the importer needs.
type Pages interface {
	Fetch(ctx context.Context, rawURL string) (*Page, error)
	DownloadImage(ctx context.Context, rawURL string) ([]byte, string, error)
}

// PageFinder looks up the pages of one specific event.
type PageFinder interface {
	FindPages(ctx context.Context, ev Event) ([]string, error)
}

// Uploader stores a flyer and returns nothing but an error.
type Uploader interface {
	Upload(ctx context.Context, key, contentType string, data []byte) error
}

// Tickets reads prices and tiers from a ticket page.
type Tickets interface {
	Fetch(ctx context.Context, title, date, location, country, ticketURL string) (source string, tiers []Tier, err error)
}

type Options struct {
	DryRun        bool
	AllowNoFlyer  bool
	AllowNoCoords bool
	Max           int // most events created per run
}

// Result is the outcome for one candidate.
type Result struct {
	Title   string
	Date    time.Time
	Venue   string
	Created bool
	ID      string
	Reason  string // why it was skipped
	Detail  string // what was found, for the report
}

type Importer struct {
	Discoverer Discoverer
	Finder     PageFinder // optional: searches for the pages of events the first pass found none for
	Geocoder   Geocoder
	Pages      Pages
	Uploader   Uploader
	Tickets    Tickets
	Store      Store
	Options    Options
	Now        func() time.Time
}

// Run discovers events in the window and stores the ones that pass every check.
func (i *Importer) Run(ctx context.Context, w Window) ([]Result, error) {
	if !i.Options.DryRun { // a dry run must not touch the database
		if err := i.Store.EnsureSchema(ctx); err != nil {
			return nil, err
		}
	}
	existing, err := i.Store.Upcoming(ctx, w.From, w.To)
	if err != nil {
		return nil, err
	}
	venues, err := i.Store.Venues(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := i.Discoverer.Discover(ctx, w)
	if err != nil {
		return nil, err
	}

	var results []Result
	var queue []*item
	for _, c := range candidates {
		ev, err := Validate(c, w)
		if err != nil {
			results = append(results, Result{Title: clean(c.Title, 140), Venue: clean(c.VenueName, 120), Reason: err.Error()})
			continue
		}
		queue = mergeInto(queue, ev, c.Sources)
	}
	sort.SliceStable(queue, func(a, b int) bool { return queue[a].ev.StartsAt.Before(queue[b].ev.StartsAt) })

	created := 0
	for _, it := range queue {
		res := Result{Title: it.ev.Title, Date: it.ev.StartsAt, Venue: it.ev.VenueName}
		if i.Options.Max > 0 && created >= i.Options.Max {
			res.Reason = "límite de eventos por corrida"
			results = append(results, res)
			continue
		}
		if dup, with := Duplicate(it.ev, "", existing); dup {
			res.Reason = "ya existe: " + with
			results = append(results, res)
			continue
		}
		id, source, detail, err := i.process(ctx, it, venues, existing)
		if err != nil {
			res.Reason = err.Error()
			log.Printf("omitido %q: %v", it.ev.Title, err)
		} else {
			res.Created, res.ID, res.Detail = true, id, detail
			existing = append(existing, Existing{ID: id, Title: it.ev.Title, Location: it.ev.VenueName, Date: it.ev.StartsAt, SourceURL: source})
			created++
		}
		results = append(results, res)
	}
	return results, nil
}

// process verifies, enriches and stores one event.
func (i *Importer) process(ctx context.Context, it *item, venues map[string]KnownVenue, existing []Existing) (id, source, detail string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ev := it.ev
	out := NewEvent{Event: ev}

	// 1. A real page about this event.
	pages, ticketPage := i.verify(ctx, ev, it.tickets, it.pages, it.sources)
	if len(pages) == 0 && i.Finder != nil {
		found, err := i.Finder.FindPages(ctx, ev)
		if err != nil {
			log.Printf("%q: búsqueda puntual falló: %v", ev.Title, err)
		}
		pages, ticketPage = i.verify(ctx, ev, nil, nil, found)
	}
	if len(pages) == 0 {
		return "", "", "", fmt.Errorf("sin página verificable del evento")
	}
	out.SourceURL = cleanURL(pages[0].FinalURL)
	if dup, with := Duplicate(ev, out.SourceURL, existing); dup {
		return "", "", "", fmt.Errorf("ya existe: %s", with)
	}
	if ticketPage != nil {
		out.TicketURL = cleanURL(ticketPage.FinalURL)
	}

	// 2. Coordinates: venues we already know first, then OpenStreetMap.
	if known, ok := venues[normalize(ev.VenueName)]; ok {
		out.Place, out.HasPlace = Place{Lat: known.Lat, Lng: known.Lng, City: known.City, Department: known.Department}, true
	} else if place, err := i.Geocoder.Geocode(ctx, ev.VenueName, ev.Address, ev.City); err != nil {
		log.Printf("geocodificando %q: %v", ev.VenueName, err)
	} else if place != nil {
		out.Place, out.HasPlace = *place, true
	}
	if !out.HasPlace && !i.Options.AllowNoCoords {
		return "", "", "", fmt.Errorf("no se pudo ubicar el venue %q", ev.VenueName)
	}

	// 3. Flyer.
	var flyer []byte
	var mime, flyerURL string
	for _, page := range pages {
		for _, img := range page.Images {
			if genericImage(img) {
				continue
			}
			data, m, err := i.Pages.DownloadImage(ctx, img)
			if err != nil {
				continue
			}
			flyer, mime, flyerURL = data, m, img
			break
		}
		if flyer != nil {
			break
		}
	}
	if flyer == nil && !i.Options.AllowNoFlyer {
		return "", "", "", fmt.Errorf("sin flyer utilizable")
	}

	// 4. Tickets, only from a page we were allowed to read.
	if ticketPage != nil {
		source, tiers, err := i.Tickets.Fetch(ctx, ev.Title, ev.StartsAt.Format("2006-01-02 15:04"), ev.VenueName, "Uruguay", out.TicketURL)
		if err != nil {
			log.Printf("entradas de %q: %v", ev.Title, err)
		} else if len(tiers) > 0 {
			out.Tiers, out.TiersFetched = tiers, true
			out.TicketSource = firstNonEmpty(source, out.TicketURL)
		}
	}
	if out.TicketSource == "" {
		out.TicketSource = out.TicketURL
	}

	if i.Options.DryRun {
		return "(dry-run)", out.SourceURL, summary(out, flyerURL), nil
	}
	if flyer != nil {
		out.BannerKey = fmt.Sprintf("events/%d_%s.%s", i.now().UnixNano(), slug(ev.Title), Extension(mime))
		if err := i.Uploader.Upload(ctx, out.BannerKey, mime, flyer); err != nil {
			return "", "", "", fmt.Errorf("subiendo flyer: %w", err)
		}
	}
	id, err = i.Store.Insert(ctx, out)
	if err != nil {
		return "", "", "", err
	}
	return id, out.SourceURL, summary(out, flyerURL), nil
}

func (i *Importer) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func uniqueURLs(urls ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// item is one distinct event with every URL found for it across searches.
type item struct {
	ev      Event
	tickets []string
	pages   []string
	sources []string // pages the searches consulted; matched to the event by code
}

// mergeInto adds ev to the queue, folding it into an event it duplicates so
// the same party found by several searches is processed once.
func mergeInto(queue []*item, ev Event, sources []string) []*item {
	for _, it := range queue {
		same, _ := Duplicate(ev, "", []Existing{{Title: it.ev.Title, Location: it.ev.VenueName, Date: it.ev.StartsAt}})
		if !same {
			continue
		}
		it.tickets = append(it.tickets, nonEmpty(ev.TicketURL)...)
		it.pages = append(it.pages, nonEmpty(ev.EventURL)...)
		it.sources = append(it.sources, sources...)
		if len(ev.Lineup) > len(it.ev.Lineup) {
			it.ev.Lineup = ev.Lineup
		}
		if it.ev.Genre == "" {
			it.ev.Genre = ev.Genre
		}
		if it.ev.Address == "" {
			it.ev.Address = ev.Address
		}
		if it.ev.City == "" {
			it.ev.City = ev.City
		}
		if it.ev.Description == "" {
			it.ev.Description = ev.Description
		}
		if it.ev.EndsAt == nil {
			it.ev.EndsAt = ev.EndsAt
		}
		return queue
	}
	return append(queue, &item{ev: ev, tickets: nonEmpty(ev.TicketURL), pages: nonEmpty(ev.EventURL), sources: sources})
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ticketHosts are ticket sellers whose event pages carry the price and flyer.
var ticketHosts = []string{
	"passline.com", "tickantel.com.uy", "redtickets.uy", "entraste.com", "eventbrite.com", "eventbrite.com.uy",
	"eventbrite.com.ar", "vamoseventos.com", "ticketek.com.uy", "venti.com.uy", "venti.live", "mientrada.com.uy",
	"mi-entrada.com.uy", "get-in.com", "ticketfacil.com.uy", "wearebombo.com",
}

func isTicketHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, h := range ticketHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// matchingSources keeps the consulted pages whose URL path names the event:
// that tells an event's own page apart from a listing or a news article.
func matchingSources(title string, sources []string) []string {
	var out []string
	for _, raw := range sources {
		if namesEvent(title, raw) {
			out = append(out, raw)
		}
	}
	return out
}

func filterURLs(urls []string, keep func(host string) bool) []string {
	var out []string
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil && keep(u.Hostname()) {
			out = append(out, raw)
		}
	}
	return out
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// verify reads the candidate pages and keeps those that really are about the
// event. Consulted sources only count if their URL names the event.
func (i *Importer) verify(ctx context.Context, ev Event, ticketURLs, pageURLs, sources []string) (pages []*Page, ticketPage *Page) {
	matched := matchingSources(ev.Title, sources)
	tickets := append(append([]string{}, ticketURLs...), filterURLs(matched, isTicketHost)...)
	others := append(append([]string{}, pageURLs...), filterURLs(matched, func(h string) bool { return !isTicketHost(h) })...)
	urls := firstN(uniqueURLs(append(append([]string{}, tickets...), others...)...), 6)
	if len(urls) == 0 {
		log.Printf("%q: no hay ninguna URL candidata", ev.Title)
	}
	for _, raw := range urls {
		if !namesEvent(ev.Title, raw) {
			log.Printf("%q: %s no es la página propia del evento (la URL no lo nombra)", ev.Title, raw)
			continue
		}
		page, err := i.Pages.Fetch(ctx, raw)
		if err != nil {
			log.Printf("%q: no se pudo leer %s: %v", ev.Title, raw, err)
			continue
		}
		if page.Article {
			log.Printf("%q: %s es una nota, no la página del evento", ev.Title, raw)
			continue
		}
		if !page.Matches(ev.Title) {
			log.Printf("%q: %s no habla de este evento (título de la página: %q)", ev.Title, raw, page.Title)
			continue
		}
		pages = append(pages, page)
		if ticketPage == nil && contains(tickets, raw) {
			ticketPage = page
		}
	}
	return pages, ticketPage
}

func summary(ev NewEvent, flyerURL string) string {
	where := "sin coordenadas"
	if ev.HasPlace {
		where = fmt.Sprintf("%.5f,%.5f %s/%s", ev.Place.Lat, ev.Place.Lng, ev.Place.City, ev.Place.Department)
	}
	return fmt.Sprintf("fuente: %s\n      flyer: %s\n      lugar: %s · entradas: %d tandas · lineup: %d", ev.SourceURL, firstNonEmpty(flyerURL, "—"), where, len(ev.Tiers), len(ev.Lineup))
}

// namesEvent reports whether a URL's path names the event, which tells the
// event's own page apart from a listing, a home page or a news article. A
// title with no identifying word only needs half its words in the path.
func namesEvent(title, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := tokens(u.Path)
	if want := distinctive(title); len(want) > 0 {
		for w := range want {
			if path[w] {
				return true
			}
		}
		return false
	}
	want := tokens(title)
	shared := 0
	for w := range want {
		if path[w] {
			shared++
		}
	}
	return len(want) > 0 && float64(shared)/float64(len(want)) >= 0.5
}

// genericImage spots a site's logo or default share image, which is never an
// event's flyer.
func genericImage(raw string) bool {
	low := strings.ToLower(raw)
	for _, marker := range []string{"og-image", "og_image", "logo", "default", "placeholder", "favicon", "/brand/", "sprite", "avatar", "banner-default"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
