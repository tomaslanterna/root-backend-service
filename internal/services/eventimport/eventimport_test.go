package eventimport

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var window = Window{
	From: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2027, 1, 16, 0, 0, 0, 0, time.UTC),
}

func TestValidateKeepsLocalWallClockTime(t *testing.T) {
	ev, err := Validate(Candidate{Title: " Veta ", VenueName: "Ébano Mvd.", StartsAt: "2026-12-05T23:00", EndsAt: "2026-12-06T05:00",
		Lineup: []string{"Eneba", "eneba", "desconocido", "Veta", " Aurelio "}}, window)
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.StartsAt.Format("2006-01-02 15:04"); got != "2026-12-05 23:00" {
		t.Fatalf("start %s", got)
	}
	if ev.EndsAt == nil || ev.EndsAt.Format("15:04") != "05:00" {
		t.Fatalf("end %v", ev.EndsAt)
	}
	if strings.Join(ev.Lineup, ",") != "Eneba,Aurelio" {
		t.Fatalf("lineup %v", ev.Lineup)
	}
	// An explicit offset is converted to Uruguay time.
	ev, err = Validate(Candidate{Title: "Veta", VenueName: "Ébano", StartsAt: "2026-12-06T02:00:00Z"}, window)
	if err != nil || ev.StartsAt.Format("2006-01-02 15:04") != "2026-12-05 23:00" {
		t.Fatalf("offset: %v %v", ev.StartsAt, err)
	}
}

func TestValidateRejectsBadCandidates(t *testing.T) {
	cases := map[string]Candidate{
		"past":        {Title: "Veta", VenueName: "X", StartsAt: "2026-11-01T22:00"},
		"too far":     {Title: "Veta", VenueName: "X", StartsAt: "2027-06-01T22:00"},
		"no venue":    {Title: "Veta", StartsAt: "2026-12-05T22:00"},
		"no title":    {VenueName: "X", StartsAt: "2026-12-05T22:00"},
		"unparseable": {Title: "Veta", VenueName: "X", StartsAt: "pronto"},
	}
	for name, c := range cases {
		if _, err := Validate(c, window); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCleanURLStripsTrackingAndRejectsOddSchemes(t *testing.T) {
	if got := cleanURL("https://a.com/e?utm_source=x&id=3#frag"); got != "https://a.com/e?id=3" {
		t.Fatal(got)
	}
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "ftp://a.com", "https://user:pw@a.com/", "", "//a.com"} {
		if cleanURL(bad) != "" {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestDuplicate(t *testing.T) {
	date := time.Date(2026, 12, 5, 23, 0, 0, 0, time.UTC)
	existing := []Existing{{Title: "Veta", Location: "Ébano Mvd.", Date: date, SourceURL: "https://t.com/veta"}}
	same := Event{Title: "VETA presenta", VenueName: "Ebano", StartsAt: date.Add(2 * time.Hour)}
	if dup, _ := Duplicate(same, "", existing); !dup {
		t.Error("same night, same title should match")
	}
	other := Event{Title: "Techno Night", VenueName: "Ébano Mvd.", StartsAt: date}
	if dup, _ := Duplicate(other, "", existing); dup {
		t.Error("a different party at the same venue is not a duplicate")
	}
	later := Event{Title: "Veta", VenueName: "Ébano Mvd.", StartsAt: date.AddDate(0, 0, 7)}
	if dup, _ := Duplicate(later, "", existing); dup {
		t.Error("the same title a week later is a different event")
	}
	if dup, _ := Duplicate(later, "https://t.com/veta", existing); !dup {
		t.Error("same source URL is always a duplicate")
	}
}

func TestRobots(t *testing.T) {
	robots := parseRobots(`
User-agent: GPTBot
Disallow: /

User-agent: *
Disallow: /ir
Disallow: /admin/
Allow: /admin/public
`)
	for path, want := range map[string]bool{"/veta": true, "/ir/1600?d=ticket": false, "/admin/x": false, "/admin/public": true, "/": true} {
		if got := robots.allows(path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
	if parseRobots("User-agent: *\nDisallow: /").allows("/anything") {
		t.Error("Disallow: / blocks everything")
	}
	if parseRobots("User-agent: * Disallow: /").allows("/x") {
		t.Error("directives glued on one line still block everything")
	}
	if !parseRobots("User-agent: *\nDisallow:").allows("/x") {
		t.Error("an empty Disallow allows everything")
	}
	ours := parseRobots("User-agent: RootEventsBot\nDisallow: /private\n\nUser-agent: *\nDisallow: /")
	if !ours.allows("/public") || ours.allows("/private") {
		t.Error("a group naming us wins over *")
	}
}

func TestParsePage(t *testing.T) {
	base, _ := url.Parse("https://tickets.example/evento/veta")
	page := parsePage(base, []byte(`<html><head><title>Fallback</title>
<meta property="og:title" content="Veta en Ébano">
<meta property="og:image" content="/img/flyer.jpg">
<meta name="twitter:image" content="https://cdn.example/t.jpg">
<script>var hidden = "no deberia verse";</script></head>
<body><h1>Veta</h1><p>Sábado 5 de diciembre en Ébano Mvd.</p></body></html>`))
	if page.Title != "Veta en Ébano" {
		t.Errorf("title %q", page.Title)
	}
	if len(page.Images) != 2 || page.Images[0] != "https://tickets.example/img/flyer.jpg" {
		t.Errorf("images %v", page.Images)
	}
	if strings.Contains(page.Text, "deberia") {
		t.Error("script text leaked")
	}
	if !page.Matches("Veta") {
		t.Error("page should match its own title")
	}
	if page.Matches("Techno Garage Party Argentina") {
		t.Error("an unrelated title must not match")
	}
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1)) // noise, so it doesn't compress below the minimum size
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestCheckImage(t *testing.T) {
	if err := checkImage(pngBytes(600, 600)); err != nil {
		t.Errorf("a real 600x600 image should pass: %v", err)
	}
	if checkImage(pngBytes(150, 150)) == nil {
		t.Error("a tiny image must be rejected")
	}
	if checkImage(bytes.Repeat([]byte("<html>"), 10000)) == nil {
		t.Error("HTML is not an image")
	}
	if ImageType(pngBytes(10, 10)) != "image/png" || Extension("image/webp") != "webp" {
		t.Error("type sniffing")
	}
}

func TestWebRefusesLocalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secret")
	}))
	defer srv.Close()
	web := NewWeb()
	if _, err := web.get(context.Background(), srv.URL, "*/*"); err == nil {
		t.Fatal("a loopback address must never be contacted")
	}
}

func TestParseNominatim(t *testing.T) {
	place := parseNominatim([]byte(`[{"lat":"-34.9011","lon":"-56.1645","address":{"state":"Departamento de Montevideo","city":"Montevideo"}}]`))
	if place == nil || place.Department != "Montevideo" || place.City != "Montevideo" {
		t.Fatalf("%+v", place)
	}
	if parseNominatim([]byte(`[{"lat":"-34.6","lon":"-58.4","address":{}}]`)) != nil {
		t.Error("Buenos Aires is outside Uruguay")
	}
	if parseNominatim([]byte(`[{"lat":"-34.4733","lon":"-57.8400","address":{"state":"Colonia"}}]`)) == nil {
		t.Error("Colonia del Sacramento is in Uruguay")
	}
	if parseNominatim([]byte(`[]`)) != nil {
		t.Error("no results")
	}
}

func TestNominatimCachesAndIdentifiesItself(t *testing.T) {
	calls, agent := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		agent = r.Header.Get("User-Agent")
		fmt.Fprint(w, `[{"lat":"-34.9","lon":"-56.1","address":{"state":"Montevideo"}}]`)
	}))
	defer srv.Close()
	n := NewNominatim()
	n.BaseURL = srv.URL
	for i := 0; i < 2; i++ {
		if place, err := n.Geocode(context.Background(), "Ébano", "", "Montevideo"); err != nil || place == nil {
			t.Fatal(place, err)
		}
	}
	if calls != 1 || agent != UserAgent {
		t.Fatalf("calls=%d agent=%q", calls, agent)
	}
}

// --- importer with fakes ---

type fakeDiscoverer struct{ out []Candidate }

func (f fakeDiscoverer) Discover(context.Context, Window) ([]Candidate, error) { return f.out, nil }

type fakeGeocoder struct{ place *Place }

func (f fakeGeocoder) Geocode(context.Context, string, string, string) (*Place, error) {
	return f.place, nil
}

type fakePages struct {
	pages map[string]*Page
	image bool
}

func (f fakePages) Fetch(_ context.Context, u string) (*Page, error) {
	if p, ok := f.pages[u]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("404")
}
func (f fakePages) DownloadImage(context.Context, string) ([]byte, string, error) {
	if !f.image {
		return nil, "", fmt.Errorf("no image")
	}
	return pngBytes(600, 600), "image/png", nil
}

type fakeUploader struct{ keys []string }

func (f *fakeUploader) Upload(_ context.Context, key, _ string, _ []byte) error {
	f.keys = append(f.keys, key)
	return nil
}

type fakeTickets struct{}

func (fakeTickets) Fetch(context.Context, string, string, string, string, string) (string, []Tier, error) {
	return "https://tickets.example/veta", []Tier{{Name: "General - Lote 1", Price: 400, Currency: "UYU"}, {Name: "VIP", Price: 900, Currency: "UYU"}}, nil
}

type fakeStore struct {
	existing []Existing
	venues   map[string]KnownVenue
	inserted []NewEvent
}

func (f *fakeStore) EnsureSchema(context.Context) error { return nil }
func (f *fakeStore) Upcoming(context.Context, time.Time, time.Time) ([]Existing, error) {
	return f.existing, nil
}
func (f *fakeStore) Venues(context.Context) (map[string]KnownVenue, error) { return f.venues, nil }
func (f *fakeStore) Insert(_ context.Context, ev NewEvent) (string, error) {
	f.inserted = append(f.inserted, ev)
	return fmt.Sprintf("id-%d", len(f.inserted)), nil
}

func newImporter(cands []Candidate, store *fakeStore, pages fakePages, place *Place, opts Options) (*Importer, *fakeUploader) {
	up := &fakeUploader{}
	return &Importer{
		Discoverer: fakeDiscoverer{cands}, Geocoder: fakeGeocoder{place}, Pages: pages, Uploader: up,
		Tickets: fakeTickets{}, Store: store, Options: opts,
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	}, up
}

func goodCandidate() Candidate {
	return Candidate{Title: "Veta", StartsAt: "2026-12-05T23:00", VenueName: "Ébano Mvd.", City: "Montevideo", Genre: "Techno",
		Lineup: []string{"Eneba", "Aurelio"}, TicketURL: "https://tickets.example/veta"}
}

func goodPages() fakePages {
	return fakePages{image: true, pages: map[string]*Page{
		"https://tickets.example/veta": {FinalURL: "https://tickets.example/veta?utm_source=ig", Title: "Veta en Ébano", Text: "veta ebano", Images: []string{"https://tickets.example/f.png"}},
	}}
}

func TestImporterCreatesAFullyEnrichedEvent(t *testing.T) {
	store := &fakeStore{}
	imp, up := newImporter([]Candidate{goodCandidate()}, store, goodPages(), &Place{Lat: -34.9, Lng: -56.16, City: "Montevideo", Department: "Montevideo"}, Options{})
	results, err := imp.Run(context.Background(), window)
	if err != nil || len(results) != 1 || !results[0].Created {
		t.Fatalf("%+v %v", results, err)
	}
	ev := store.inserted[0]
	if ev.SourceURL != "https://tickets.example/veta" || ev.TicketURL != "https://tickets.example/veta" {
		t.Errorf("urls %q %q", ev.SourceURL, ev.TicketURL)
	}
	if !ev.HasPlace || ev.Place.Department != "Montevideo" || len(ev.Tiers) != 2 || !strings.HasPrefix(ev.BannerKey, "events/") || !strings.HasSuffix(ev.BannerKey, ".png") {
		t.Errorf("%+v", ev)
	}
	if len(up.keys) != 1 || up.keys[0] != ev.BannerKey {
		t.Errorf("uploads %v", up.keys)
	}
}

func TestImporterDryRunWritesNothing(t *testing.T) {
	store := &fakeStore{}
	imp, up := newImporter([]Candidate{goodCandidate()}, store, goodPages(), &Place{Lat: -34.9, Lng: -56.16}, Options{DryRun: true})
	results, _ := imp.Run(context.Background(), window)
	if !results[0].Created || len(store.inserted) != 0 || len(up.keys) != 0 {
		t.Fatalf("dry run wrote data: %+v %v %v", results, store.inserted, up.keys)
	}
}

func TestImporterSkipsWhatItCannotVerify(t *testing.T) {
	place := &Place{Lat: -34.9, Lng: -56.16}
	cases := map[string]struct {
		mutate func(*Candidate)
		pages  fakePages
		place  *Place
		want   string
	}{
		"no page":        {func(c *Candidate) { c.TicketURL = "https://nope.example/x" }, goodPages(), place, "sin página verificable"},
		"unrelated page": {func(c *Candidate) {}, fakePages{image: true, pages: map[string]*Page{"https://tickets.example/veta": {FinalURL: "https://tickets.example/veta", Title: "Otra cosa", Text: "nada que ver"}}}, place, "sin página verificable"},
		"no flyer":       {func(c *Candidate) {}, fakePages{image: false, pages: goodPages().pages}, place, "sin flyer"},
		"no coordinates": {func(c *Candidate) {}, goodPages(), nil, "no se pudo ubicar"},
	}
	for name, tc := range cases {
		c := goodCandidate()
		tc.mutate(&c)
		store := &fakeStore{}
		imp, _ := newImporter([]Candidate{c}, store, tc.pages, tc.place, Options{})
		results, _ := imp.Run(context.Background(), window)
		if results[0].Created || !strings.Contains(results[0].Reason, tc.want) || len(store.inserted) != 0 {
			t.Errorf("%s: %+v", name, results[0])
		}
	}
}

func TestImporterReusesKnownVenueAndSkipsDuplicates(t *testing.T) {
	store := &fakeStore{
		venues:   map[string]KnownVenue{normalize("Ébano Mvd."): {Lat: -34.91, Lng: -56.17, Department: "Montevideo"}},
		existing: []Existing{{Title: "Aniversario", Location: "Phonotheque", Date: time.Date(2026, 12, 5, 23, 0, 0, 0, time.UTC)}},
	}
	dup := goodCandidate()
	dup.Title, dup.VenueName = "Aniversario Phonotheque", "Phonotheque"
	imp, _ := newImporter([]Candidate{goodCandidate(), dup, goodCandidate()}, store, goodPages(), nil, Options{})
	results, _ := imp.Run(context.Background(), window)
	created := 0
	for _, r := range results {
		if r.Created {
			created++
		}
	}
	if created != 1 || len(store.inserted) != 1 || store.inserted[0].Place.Lat != -34.91 {
		t.Fatalf("%+v inserted=%+v", results, store.inserted)
	}
}

func TestImporterHonoursMax(t *testing.T) {
	a, b := goodCandidate(), goodCandidate()
	b.Title, b.StartsAt = "Otra Noche", "2026-12-12T23:00"
	pages := goodPages()
	pages.pages["https://tickets.example/otra"] = &Page{FinalURL: "https://tickets.example/otra", Title: "Otra Noche", Text: "otra noche", Images: []string{"i"}}
	b.TicketURL = "https://tickets.example/otra"
	store := &fakeStore{}
	imp, _ := newImporter([]Candidate{a, b}, store, pages, &Place{Lat: -34.9, Lng: -56.1}, Options{Max: 1})
	imp.Run(context.Background(), window)
	if len(store.inserted) != 1 {
		t.Fatalf("inserted %d", len(store.inserted))
	}
}

func TestMatchingSourcesPicksTheEventsOwnPage(t *testing.T) {
	sources := []string{
		"https://www.passline.com/eventos/circusland-pres-one-night-in-the-jungle",
		"https://laproximafecha.com/montevideo",
		"https://revendiste.com/eventos/21-11-wasabi-fest-en-montevideo-music-box",
	}
	got := matchingSources("CIRCUSLAND pres. ONE NIGHT IN THE JUNGLE", sources)
	if len(got) != 1 || !strings.Contains(got[0], "passline") {
		t.Fatalf("%v", got)
	}
	if !isTicketHost("www.passline.com") || isTicketHost("laproximafecha.com") || isTicketHost("evilpassline.com") {
		t.Error("ticket host detection")
	}
}

func TestSameEventFoundBySeveralSearchesIsProcessedOnce(t *testing.T) {
	a, b := goodCandidate(), goodCandidate()
	b.Title, b.TicketURL = "VETA presenta", ""
	b.Sources = []string{"https://tickets.example/veta"}
	store := &fakeStore{}
	imp, _ := newImporter([]Candidate{a, b}, store, goodPages(), &Place{Lat: -34.9, Lng: -56.1}, Options{})
	results, _ := imp.Run(context.Background(), window)
	if len(results) != 1 || !results[0].Created {
		t.Fatalf("%+v", results)
	}
}

func TestEventPageIsFoundFromSearchSourcesWhenGeminiGivesNoURL(t *testing.T) {
	c := goodCandidate()
	c.TicketURL = ""
	c.Sources = []string{"https://tickets.example/otra-cosa", "https://tickets.example/veta"}
	pages := goodPages()
	store := &fakeStore{}
	imp, _ := newImporter([]Candidate{c}, store, pages, &Place{Lat: -34.9, Lng: -56.1}, Options{})
	results, _ := imp.Run(context.Background(), window)
	if !results[0].Created {
		t.Fatalf("%+v", results[0])
	}
}

type fakeFinder struct{ urls []string }

func (f fakeFinder) FindPages(context.Context, Event) ([]string, error) { return f.urls, nil }

func TestFinderIsUsedWhenTheFirstPassFindsNoPage(t *testing.T) {
	c := goodCandidate()
	c.TicketURL = ""
	store := &fakeStore{}
	imp, _ := newImporter([]Candidate{c}, store, goodPages(), &Place{Lat: -34.9, Lng: -56.1}, Options{})
	results, _ := imp.Run(context.Background(), window)
	if results[0].Created {
		t.Fatal("without any URL it must be skipped")
	}
	imp.Finder = fakeFinder{[]string{"https://tickets.example/veta", "https://tickets.example/otra-cosa"}}
	results, _ = imp.Run(context.Background(), window)
	if !results[0].Created {
		t.Fatalf("the finder's page should rescue it: %+v", results[0])
	}
}

// The cases below are real mistakes from the first production run.
func TestDuplicatesThatSlippedThroughInProduction(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 12, d, h, 0, 0, 0, time.UTC) }
	cases := []struct {
		name     string
		ev       Event
		existing Existing
	}{
		{"unreal", Event{Title: "19:26 Afterlife - UNREAL 2nd Anniversary", VenueName: "The Club Cube", StartsAt: time.Date(2026, 11, 27, 19, 26, 0, 0, time.UTC)},
			Existing{Title: "Unreal 2nd anniversary", Location: "The Cube", Date: time.Date(2026, 11, 27, 23, 0, 0, 0, time.UTC)}},
		{"anotr", Event{Title: "ANOTR 28 de diciembre - Cipriani, Punta del Este", VenueName: "Cipriani", StartsAt: at(28, 23)},
			Existing{Title: "ANOTR - SUNSET", Location: "CIPRIANI", Date: at(28, 17)}},
		{"unique", Event{Title: "Punta Verano: Réveillon Unique", VenueName: "Serena Hotel, Mansa Beach", StartsAt: at(31, 23)},
			Existing{Title: "REVEILLON UNIQUE", Location: "SERENA HOTEL", Date: at(31, 23)}},
		{"jamie jones", Event{Title: "Réveillon Punta NYE: Jamie Jones", VenueName: "Museo Atchugarry", StartsAt: at(31, 22)},
			Existing{Title: "REVEILLON PUNTA - FRANKY RIZARDO / JAMIE JONES", Location: "MACA", Date: at(31, 23)}},
	}
	for _, c := range cases {
		if dup, _ := Duplicate(c.ev, "", []Existing{c.existing}); !dup {
			t.Errorf("%s should have been recognised as a duplicate", c.name)
		}
	}
	// Two different parties on the same night must stay apart.
	a := Event{Title: "Hot Since 82 NYE", VenueName: "Medellín Polo Club", StartsAt: at(31, 23)}
	if dup, _ := Duplicate(a, "", []Existing{{Title: "Huma NYE", Location: "Huma Rooftop", Date: at(31, 23)}}); dup {
		t.Error("different parties are not duplicates")
	}
	if dup, _ := Duplicate(a, "", []Existing{{Title: "Aura NYE", Location: "Medellín Polo Club", Date: at(31, 23)}}); dup {
		t.Error("sharing only generic words (NYE) at the same venue is not enough")
	}
}

func TestOnlyAnEventsOwnPageCounts(t *testing.T) {
	yes := map[string]string{
		"https://app.entraste.com/events/fantasy-nye-2027":                    "Fantasy NYE",
		"https://www.passline.com/eventos/la-tm-new-years-eve-punta-del-este": "LA TM - AÑO NUEVO, PUNTA DEL ESTE (+30)",
		"https://www.passline.com/eventos/stereo-club-pres-real-techno-night": "STEREO CLUB pres. REAL TECHNO NIGHT",
	}
	for u, title := range yes {
		if !namesEvent(title, u) {
			t.Errorf("%s should name %q", u, title)
		}
	}
	no := map[string]string{
		"https://allmusicparties.com/es/eventos": "ANOTR 28 de diciembre - Cipriani, Punta del Este",
		"https://www.infobae.com/inhouse/2025/12/26/punta-del-este-2026-dos-encuentros-musicales-marcan-el-pulso-cultural-del-verano/": "Punta Verano presenta Hed Kandi",
		"https://estilopunta.com/que-hacer-en-punta-del-este-en-el-verano-2027-la-agenda-de-eventos-de-diciembre-a-marzo/":             "Punta Verano: Réveillon Unique",
	}
	for u, title := range no {
		if namesEvent(title, u) {
			t.Errorf("%s is not the page of %q", u, title)
		}
	}
}

func TestGenericImagesAndArticlesAreNeverFlyers(t *testing.T) {
	for _, u := range []string{"https://allmusicparties.com/brand/og-image.png", "https://x.com/assets/logo.png", "https://x.com/img/default.jpg"} {
		if !genericImage(u) {
			t.Errorf("%s is not a flyer", u)
		}
	}
	if genericImage("https://www.passline.com/imagenes/eventos/-826271-rec.jpg") {
		t.Error("a real flyer was rejected")
	}
	base, _ := url.Parse("https://news.example/nota")
	page := parsePage(base, []byte(`<head><meta property="og:type" content="article"><title>Nota</title></head>`))
	if !page.Article {
		t.Error("og:type article must be detected")
	}
	pages := goodPages()
	pages.pages["https://tickets.example/veta"] = &Page{FinalURL: "https://tickets.example/veta", Title: "Veta", Text: "veta", Article: true, Images: []string{"https://tickets.example/f.png"}}
	store := &fakeStore{}
	imp, _ := newImporter([]Candidate{goodCandidate()}, store, pages, &Place{Lat: -34.9, Lng: -56.1}, Options{})
	if results, _ := imp.Run(context.Background(), window); results[0].Created {
		t.Error("an article must not be accepted as the event page")
	}
}

func TestReferralCodesAndBlockedSitesStayOut(t *testing.T) {
	if got := cleanURL("https://app.entraste.com/events/fantasy-nye-2027?rrpp=7FELIREBELLO1697&utm_source=ig"); got != "https://app.entraste.com/events/fantasy-nye-2027" {
		t.Errorf("referral code kept: %s", got)
	}
	BlockedHosts = []string{"blocked.example"}
	defer func() { BlockedHosts = nil }()
	web := NewWeb()
	for _, u := range []string{"https://blocked.example/x", "https://www.blocked.example/y"} {
		if web.Allowed(context.Background(), u) {
			t.Errorf("%s must never be read", u)
		}
	}
}

func TestRetriesAfterTooManyRequests(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	web := NewWeb()
	// the test server is on loopback, which Web refuses: swap in a plain client
	web.client = srv.Client()
	resp, err := web.get(context.Background(), srv.URL, "*/*")
	if err != nil || resp.StatusCode != 200 || calls != 2 {
		t.Fatalf("status=%v calls=%d err=%v", resp, calls, err)
	}
	if retryAfter("120", time.Second) != 30*time.Second || retryAfter("", 7*time.Second) != 7*time.Second {
		t.Error("retry delay")
	}
}
