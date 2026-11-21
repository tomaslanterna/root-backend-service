package eventimport

import (
	"regexp"
	"strings"
)

// inlineDirective finds directives glued onto one line, like
// "User-agent: * Disallow: /", which we read as separate lines.
var inlineDirective = regexp.MustCompile(`(?i)\s+(user-agent|disallow|allow|sitemap)\s*:`)

type robotsRule struct {
	allow bool
	path  string
}

type robotsRules struct {
	denyAll bool
	rules   []robotsRule
}

// parseRobots keeps the rules that apply to RootEventsBot, or to every bot
// when no group names it.
func parseRobots(body string) *robotsRules {
	own, all := &robotsRules{}, &robotsRules{}
	var agents []string
	inRules := false
	body = inlineDirective.ReplaceAllString(strings.ReplaceAll(body, "\r", ""), "\n$1:")
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if inRules {
				agents, inRules = nil, false
			}
			agents = append(agents, strings.ToLower(value))
		case "allow", "disallow":
			inRules = true
			rule := robotsRule{allow: key == "allow", path: value}
			for _, agent := range agents {
				if agent == "*" {
					all.rules = append(all.rules, rule)
				} else if agent != "" && strings.Contains(strings.ToLower(UserAgent), agent) {
					own.rules = append(own.rules, rule)
				}
			}
		}
	}
	if len(own.rules) > 0 {
		return own
	}
	return all
}

// allows applies the longest matching rule; Allow wins ties.
func (r *robotsRules) allows(path string) bool {
	if r.denyAll {
		return false
	}
	if path == "" {
		path = "/"
	}
	best, allowed := -1, true
	for _, rule := range r.rules {
		if rule.path == "" || !robotsMatch(rule.path, path) {
			continue
		}
		if n := len(rule.path); n > best || (n == best && rule.allow) {
			best, allowed = n, rule.allow
		}
	}
	return allowed
}

func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, part := range parts[1:] {
		i := strings.Index(path[pos:], part)
		if i < 0 {
			return false
		}
		pos += i + len(part)
	}
	return !anchored || pos == len(path)
}
