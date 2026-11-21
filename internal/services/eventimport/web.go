package eventimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

// UserAgent identifies the importer to the sites it reads.
const UserAgent = "RootEventsBot/1.0"

const (
	maxPageBytes  = 1 << 20
	maxImageBytes = 8 << 20
	minImageBytes = 15 << 10
)

// Page is what we keep from a fetched event or ticket page.
type Page struct {
	FinalURL string
	Title    string
	Article  bool     // og:type says this is a news/blog article, not an event page
	Images   []string // og:image / twitter:image, absolute, best first
	Text     string   // visible text, lowercased and normalized
}

// Web fetches pages and images. URLs come from an LLM, so every connection
// refuses private addresses and every host's robots.txt is honoured.
type Web struct {
	client *http.Client
	mu     sync.Mutex
	robots map[string]*robotsRules
	last   map[string]time.Time
}

func NewWeb() *Web {
	dialer := &net.Dialer{
		Timeout: 8 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("dirección no permitida: %s", host)
			}
			return nil
		},
	}
	return &Web{
		robots: map[string]*robotsRules{},
		last:   map[string]time.Time{},
		client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: &http.Transport{DialContext: dialer.DialContext, MaxIdleConns: 10, ResponseHeaderTimeout: 15 * time.Second},
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("demasiadas redirecciones")
				}
				return nil
			},
		},
	}
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.Equal(net.IPv4bcast))
}

// get issues a request politely: at most one per host every 1.5 s, and a
// 429/503 is retried after the server's Retry-After (capped), twice at most.
func (w *Web) get(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("URL no válida: %q", rawURL)
	}
	for attempt := 0; ; attempt++ {
		if err := w.wait(ctx, u.Hostname()); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", accept)
		resp, err := w.client.Do(req)
		if err != nil || attempt >= 2 || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable) {
			return resp, err
		}
		delay := retryAfter(resp.Header.Get("Retry-After"), time.Duration(5*(attempt+1))*time.Second)
		resp.Body.Close()
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// retryAfter reads a Retry-After in seconds, capped at 30 s.
func retryAfter(header string, fallback time.Duration) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs >= 0 {
		fallback = time.Duration(secs) * time.Second
	}
	if fallback > 30*time.Second {
		return 30 * time.Second
	}
	return fallback
}

// wait spaces requests to the same host.
func (w *Web) wait(ctx context.Context, host string) error {
	w.mu.Lock()
	next := w.last[host].Add(1500 * time.Millisecond)
	if now := time.Now(); next.Before(now) {
		next = now
	}
	w.last[host] = next
	w.mu.Unlock()
	select {
	case <-time.After(time.Until(next)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BlockedHosts are never read, whatever their robots.txt says.
var BlockedHosts = []string{}

func blockedHost(host string) bool {
	host = strings.ToLower(host)
	for _, b := range BlockedHosts {
		if host == b || strings.HasSuffix(host, "."+b) {
			return true
		}
	}
	return false
}

// Allowed reports whether we may read rawURL: not a blocked host and allowed
// by the host's robots.txt.
func (w *Web) Allowed(ctx context.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || blockedHost(u.Hostname()) {
		return false
	}
	key := u.Scheme + "://" + u.Host
	w.mu.Lock()
	rules, ok := w.robots[key]
	w.mu.Unlock()
	if !ok {
		rules = w.loadRobots(ctx, key)
		w.mu.Lock()
		w.robots[key] = rules
		w.mu.Unlock()
	}
	return rules.allows(u.EscapedPath() + queryPart(u))
}

func queryPart(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

func (w *Web) loadRobots(ctx context.Context, origin string) *robotsRules {
	resp, err := w.get(ctx, origin+"/robots.txt", "text/plain")
	if err != nil {
		return &robotsRules{denyAll: true} // can't tell: stay out
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return &robotsRules{denyAll: true}
	case resp.StatusCode >= 400:
		return &robotsRules{} // no robots.txt: everything is allowed
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	return parseRobots(string(body))
}

// Fetch downloads an HTML page, if robots.txt allows it.
func (w *Web) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	if !w.Allowed(ctx, rawURL) {
		return nil, fmt.Errorf("robots.txt no permite leer %s", rawURL)
	}
	resp, err := w.get(ctx, rawURL, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s respondió %d", rawURL, resp.StatusCode)
	}
	if !w.Allowed(ctx, resp.Request.URL.String()) { // a redirect may leave the allowed host
		return nil, fmt.Errorf("robots.txt no permite leer %s", resp.Request.URL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, err
	}
	return parsePage(resp.Request.URL, body), nil
}

// ResolveRedirect follows Google's grounding redirect to the real page URL
// without reading the target page.
func (w *Web) ResolveRedirect(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() != "vertexaisearch.cloud.google.com" {
		return rawURL, nil
	}
	client := *w.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "" {
		return cleanURL(loc), nil
	}
	return "", errors.New("redirección sin destino")
}

// DownloadImage fetches an image and checks it is really a usable flyer.
func (w *Web) DownloadImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	if !w.Allowed(ctx, rawURL) {
		return nil, "", fmt.Errorf("robots.txt no permite leer %s", rawURL)
	}
	resp, err := w.get(ctx, rawURL, "image/jpeg,image/png,image/webp")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("imagen respondió %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	return data, ImageType(data), checkImage(data)
}

// checkImage accepts real JPEG/PNG/WebP files big enough to be a flyer.
func checkImage(data []byte) error {
	if len(data) > maxImageBytes {
		return errors.New("imagen demasiado grande")
	}
	if len(data) < minImageBytes {
		return errors.New("imagen demasiado chica para ser un flyer")
	}
	switch ImageType(data) {
	case "image/jpeg", "image/png":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("imagen ilegible: %w", err)
		}
		if cfg.Width < 400 || cfg.Height < 400 {
			return fmt.Errorf("imagen de %dx%d, muy chica", cfg.Width, cfg.Height)
		}
	case "image/webp":
	default:
		return errors.New("el archivo no es una imagen JPEG, PNG o WebP")
	}
	return nil
}

// ImageType sniffs the real type of data from its bytes.
func ImageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// Extension returns the file extension for an image MIME type.
func Extension(mime string) string {
	return map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[mime]
}

func parsePage(base *url.URL, body []byte) *Page {
	page := &Page{FinalURL: base.String()}
	var text strings.Builder
	var title, ogTitle string
	inTitle, skip := false, 0
	z := html.NewTokenizer(bytes.NewReader(body))
	add := func(raw string) {
		if ref, err := base.Parse(strings.TrimSpace(raw)); err == nil && (ref.Scheme == "http" || ref.Scheme == "https") {
			page.Images = append(page.Images, ref.String())
		}
	}
	var twitter []string
	for {
		switch z.Next() {
		case html.ErrorToken:
			page.Title = clean(firstNonEmpty(ogTitle, title), 200)
			page.Images = append(page.Images, twitter...)
			page.Text = normalize(text.String())
			return page
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "script", "style", "noscript", "svg":
				skip++
			case "title":
				inTitle = true
			case "meta":
				attrs := map[string]string{}
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					attrs[string(k)] = string(v)
				}
				prop := strings.ToLower(firstNonEmpty(attrs["property"], attrs["name"]))
				switch prop {
				case "og:image", "og:image:url", "og:image:secure_url":
					add(attrs["content"])
				case "twitter:image", "twitter:image:src":
					if ref, err := base.Parse(attrs["content"]); err == nil && attrs["content"] != "" {
						twitter = append(twitter, ref.String())
					}
				case "og:title":
					ogTitle = attrs["content"]
				case "og:type":
					page.Article = strings.EqualFold(strings.TrimSpace(attrs["content"]), "article")
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "noscript", "svg":
				if skip > 0 {
					skip--
				}
			case "title":
				inTitle = false
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			} else if skip == 0 && text.Len() < 200_000 {
				text.Write(z.Text())
				text.WriteByte(' ')
			}
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Matches reports whether the page talks about the event: at least half of
// the title's meaningful words appear in the page's title or text.
func (p *Page) Matches(title string) bool {
	want := tokens(title)
	if len(want) == 0 {
		return false
	}
	haystack := " " + normalize(p.Title) + " " + p.Text + " "
	found := 0
	for w := range want {
		if strings.Contains(haystack, " "+w+" ") {
			found++
		}
	}
	return float64(found)/float64(len(want)) >= 0.5
}
