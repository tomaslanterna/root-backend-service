package eventimport

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var stopwords = map[string]bool{
	"de": true, "la": true, "el": true, "en": true, "los": true, "las": true, "y": true, "a": true,
	"con": true, "w": true, "presenta": true, "pres": true, "presents": true, "feat": true, "ft": true,
	"vol": true, "the": true, "and": true, "mvd": true, "uy": true, "party": true, "fiesta": true,
}

// normalize lowercases, strips accents and collapses everything that is not a
// letter or digit into single spaces.
func normalize(s string) string {
	var b strings.Builder
	space := true
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// tokens returns the meaningful words of s.
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(normalize(s)) {
		if len(w) > 1 && !stopwords[w] {
			out[w] = true
		}
	}
	return out
}

// similarity is the Jaccard index of the meaningful words of a and b.
func similarity(a, b string) float64 {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	shared := 0
	for w := range ta {
		if tb[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(ta)+len(tb)-shared)
}

// slug builds a short filename-safe version of s.
func slug(s string) string {
	out := strings.ReplaceAll(normalize(s), " ", "-")
	if len(out) > 60 {
		out = strings.TrimRight(out[:60], "-")
	}
	if out == "" {
		return "evento"
	}
	return out
}

// generic words say what kind of party it is, not which one.
var generic = map[string]bool{
	"punta": true, "este": true, "del": true, "verano": true, "nye": true, "ano": true, "nuevo": true,
	"new": true, "year": true, "years": true, "eve": true, "festival": true, "club": true, "night": true,
	"noche": true, "sunset": true, "session": true, "sessions": true, "closing": true, "opening": true,
	"show": true, "showcase": true, "techno": true, "house": true, "music": true, "uruguay": true,
	"montevideo": true, "pde": true, "edition": true, "aniversario": true, "anniversary": true,
}

// distinctive returns the words of s that identify an event: not stopwords,
// not generic party words and not years.
func distinctive(s string) map[string]bool {
	out := map[string]bool{}
	for w := range tokens(s) {
		if generic[w] || (len(w) == 4 && strings.HasPrefix(w, "20")) {
			continue
		}
		out[w] = true
	}
	return out
}

// sharesDistinctive reports whether a and b have an identifying word in common.
func sharesDistinctive(a, b string) bool {
	tb := distinctive(b)
	for w := range distinctive(a) {
		if tb[w] {
			return true
		}
	}
	return false
}

// sameVenue is true when the names are close, or one is contained in the other
// ("The Cube" / "The Club Cube").
func sameVenue(a, b string) bool {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if similarity(a, b) >= 0.5 {
		return true
	}
	return subset(ta, tb) || subset(tb, ta)
}

func subset(small, big map[string]bool) bool {
	for w := range small {
		if !big[w] {
			return false
		}
	}
	return true
}
