package eventimport

import "time"

// Duplicate reports whether ev is already stored, and which event it matches.
// Same source URL always matches; otherwise the event must fall within a day
// of an existing one and look like it by title, or share the venue and a
// word that identifies the party.
func Duplicate(ev Event, sourceURL string, existing []Existing) (bool, string) {
	for _, e := range existing {
		if sourceURL != "" && e.SourceURL == sourceURL {
			return true, e.Title
		}
		gap := ev.StartsAt.Sub(e.Date)
		if gap < 0 {
			gap = -gap
		}
		if gap > 24*time.Hour {
			continue
		}
		if similarity(ev.Title, e.Title) >= 0.5 {
			return true, e.Title
		}
		// Same night at the same venue and an identifying word in common.
		if sameVenue(ev.VenueName, e.Location) && sharesDistinctive(ev.Title, e.Title) {
			return true, e.Title
		}
	}
	return false, ""
}
