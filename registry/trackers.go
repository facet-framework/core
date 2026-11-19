package facet

import (
	"fmt"
	"regexp"
	"strings"
)

// Tracker is one entry of the 'trackers:' list in .facetrc.yaml: where a
// project keeps its issues, how a key from there looks, and how to link to it.
//
// @facet: registry/layout/ticket-trackers
// @facet-role: primary
type Tracker struct {
	ID      string `yaml:"id"`
	Pattern string `yaml:"pattern"`
	URL     string `yaml:"url"`
	Default bool   `yaml:"default"`

	re *regexp.Regexp
}

// Trackers returns the project's trackers in declaration order, which is the
// order patterns are tried in. Empty for a nil Config.
func (c *Config) Trackers() []Tracker {
	if c == nil {
		return nil
	}
	return c.TrackerList
}

// compileTrackers checks the 'trackers:' list once, at load time. A pattern
// that does not compile would otherwise become a tracker that silently never
// matches — the same shape as a test that finds nothing and reads as green.
func compileTrackers(list []Tracker) error {
	seen := map[string]bool{}
	defaults := 0
	for i := range list {
		t := &list[i]
		if t.ID == "" {
			return fmt.Errorf("trackers[%d]: id is required", i)
		}
		if seen[t.ID] {
			return fmt.Errorf("trackers: id %q is declared twice", t.ID)
		}
		seen[t.ID] = true
		if t.Default {
			defaults++
		}
		if t.Pattern != "" {
			re, err := regexp.Compile(t.Pattern)
			if err != nil {
				return fmt.Errorf("trackers: %q: pattern does not compile: %w", t.ID, err)
			}
			t.re = re
		}
	}
	if defaults > 1 {
		return fmt.Errorf("trackers: %d trackers are marked default; at most one may be", defaults)
	}
	return nil
}

// ResolvedTicket says which tracker a key belongs to and where it lives.
type ResolvedTicket struct {
	Key       string `json:"key"`
	Tracker   string `json:"tracker,omitempty"`
	URL       string `json:"url,omitempty"`
	ByDefault bool   `json:"by_default,omitempty"`
}

// ResolveTicket assigns a key to a tracker: the one the reference names, else
// the first whose pattern matches, else the default. With no tracker found it
// returns the key alone — never an invented link.
func ResolveTicket(trackers []Tracker, key, named string) ResolvedTicket {
	out := ResolvedTicket{Key: key}
	if named != "" {
		for _, t := range trackers {
			if t.ID == named {
				out.Tracker, out.URL = t.ID, expand(t, key, nil)
				return out
			}
		}
		return out
	}
	for _, t := range trackers {
		if t.re == nil {
			continue
		}
		if m := t.re.FindStringSubmatch(key); m != nil {
			out.Tracker, out.URL = t.ID, expand(t, key, m)
			return out
		}
	}
	for _, t := range trackers {
		if t.Default {
			out.Tracker, out.URL, out.ByDefault = t.ID, expand(t, key, nil), true
			return out
		}
	}
	return out
}

// expand fills a URL template: {key} is the whole key, and every named group
// of the pattern is available by its name ({repo}, {number}).
func expand(t Tracker, key string, match []string) string {
	if t.URL == "" {
		return ""
	}
	url := strings.ReplaceAll(t.URL, "{key}", key)
	if t.re != nil {
		if match == nil {
			match = t.re.FindStringSubmatch(key)
		}
		if match != nil {
			for i, name := range t.re.SubexpNames() {
				if name != "" && i < len(match) {
					url = strings.ReplaceAll(url, "{"+name+"}", match[i])
				}
			}
		}
	}
	return url
}
