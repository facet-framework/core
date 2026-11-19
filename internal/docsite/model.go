// @facet: navigation/documentation/guided-documentation
// @facet-role: primary

// Package docsite builds audience-guided static documentation from FACET evidence.
package docsite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/facet-framework/core/registry"
)

const Version = 1

type Source struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}
type Evidence struct {
	ID        string         `json:"id"`
	Registry  string         `json:"registry"`
	Category  string         `json:"category"`
	Topic     string         `json:"topic"`
	Summary   string         `json:"summary"`
	Lifecycle string         `json:"lifecycle"`
	Fields    map[string]any `json:"fields"`
	Sources   []Source       `json:"sources"`
	Anchors   []facet.Anchor `json:"anchors"`
	Gaps      []string       `json:"gaps"`
}
type Inventory struct {
	Version int        `json:"version"`
	Records []Evidence `json:"records"`
}
type Audience struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Features    []string `json:"features"`
}
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}
type PerspectiveNote struct {
	Perspective string   `json:"perspective"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Sources     []string `json:"sources"`
}
type Topic struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Prefixes    []string `json:"prefixes"`
}
type FeatureLabel struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}
type Section struct {
	Items   []string `json:"items,omitempty"`
	Code    string   `json:"code,omitempty"`
	Table   *Table   `json:"table,omitempty"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Sources []string `json:"sources"`
}
type Diagram struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Nodes       []Node   `json:"nodes"`
	Edges       []Edge   `json:"edges"`
	Sources     []string `json:"sources"`
}
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}
type Article struct {
	Description string            `json:"description,omitempty"`
	Topic       string            `json:"topic,omitempty"`
	Notes       []PerspectiveNote `json:"perspective_notes,omitempty"`
	Diagram     *Diagram          `json:"diagram,omitempty"`
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Kind        string            `json:"kind"`
	Audience    string            `json:"audience"`
	Category    string            `json:"category"`
	Tags        []string          `json:"tags"`
	Features    []string          `json:"features"`
	Sections    []Section         `json:"sections"`
	Order       int               `json:"order"`
}
type Config struct {
	Mode          string                  `json:"mode,omitempty"`
	Language      string                  `json:"language,omitempty"`
	Catalog       bool                    `json:"catalog,omitempty"`
	Topics        []Topic                 `json:"topics,omitempty"`
	FeatureLabels map[string]FeatureLabel `json:"feature_labels,omitempty"`
	Visibility    string                  `json:"visibility,omitempty"`
	Version       int                     `json:"version"`
	Title         string                  `json:"title"`
	Description   string                  `json:"description"`
	Audiences     []Audience              `json:"audiences"`
	Articles      []Article               `json:"articles"`
}
type Cluster struct {
	Audience string   `json:"audience"`
	Topic    string   `json:"topic"`
	Features []string `json:"features"`
}
type Plan struct {
	Version    int                 `json:"version"`
	Clusters   []Cluster           `json:"clusters"`
	Gaps       map[string][]string `json:"gaps"`
	Unassigned []string            `json:"unassigned"`
}

func digest(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func Key(reg, id string) string {
	if reg == "" {
		return id
	}
	return reg + ":" + id
}
func text(m map[string]any, k string) string           { s, _ := m[k].(string); return s }
func object(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func array(m map[string]any, k string) []any           { v, _ := m[k].([]any); return v }

// Collect uses the registry's own configuration and anchor scanner. It does not
// interpret a declared test reference as proof that a test was executed.
func Collect(root string) (Inventory, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return Inventory{}, err
	}
	out := Inventory{Version: Version, Records: []Evidence{}}
	regs, err := facet.OpenAll(root)
	if err != nil {
		return out, err
	}
	for _, reg := range regs {
		anchors, err := facet.ScanAnchors(root, reg.ScanOptions())
		if err != nil {
			return out, err
		}
		for _, cat := range facet.Categories {
			records, err := reg.LoadCategory(cat)
			if err != nil {
				return out, err
			}
			for _, r := range records {
				if r.ID == "" {
					return out, fmt.Errorf("record without id: %s", r.Path)
				}
				ev := Evidence{ID: Key(reg.Name, r.ID), Registry: reg.Name, Category: cat, Summary: r.Intent(), Lifecycle: r.Lifecycle, Fields: r.Fields, Sources: []Source{}, Anchors: []facet.Anchor{}, Gaps: []string{}}
				bits := strings.Split(r.ID, "/")
				if len(bits) > 1 {
					ev.Topic = strings.Join(bits[:2], "/")
				} else {
					ev.Topic = cat
				}
				paths := map[string]bool{r.Path: true}
				for _, a := range anchors {
					if a.ID == r.ID && a.BelongsTo(reg.Name) {
						path := a.Path
						if !filepath.IsAbs(path) {
							path = filepath.Join(root, path)
						}
						rel, e := filepath.Rel(root, path)
						if e != nil {
							return out, e
						}
						a.Path = filepath.ToSlash(rel)
						ev.Anchors = append(ev.Anchors, a)
						paths[path] = true
					}
				}
				for path := range paths {
					resolved, e := filepath.EvalSymlinks(path)
					if e != nil {
						return out, e
					}
					realRoot, e := filepath.EvalSymlinks(root)
					if e != nil {
						return out, e
					}
					safe, e := filepath.Rel(realRoot, resolved)
					if e != nil || safe == ".." || strings.HasPrefix(safe, ".."+string(filepath.Separator)) {
						return out, fmt.Errorf("source symlink outside root: %s", path)
					}
					rel, err := filepath.Rel(root, path)
					if err != nil {
						return out, err
					}
					if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						return out, fmt.Errorf("source outside root: %s", path)
					}
					sum, err := digest(path)
					if err != nil {
						return out, err
					}
					ev.Sources = append(ev.Sources, Source{filepath.ToSlash(rel), sum})
				}
				sort.Slice(ev.Sources, func(i, j int) bool { return ev.Sources[i].Path < ev.Sources[j].Path })
				if cat == "features" {
					for _, slot := range []string{"prerequisite_knowledge", "not_for", "discoverability", "common_pitfalls", "success_signal"} {
						if strings.TrimSpace(text(object(object(r.Fields, "knowledge"), slot), "answer")) == "" {
							ev.Gaps = append(ev.Gaps, slot)
						}
					}
				}
				out.Records = append(out.Records, ev)
			}
		}
	}
	sort.Slice(out.Records, func(i, j int) bool { return out.Records[i].ID < out.Records[j].ID })
	seen := map[string]bool{}
	for _, r := range out.Records {
		if seen[r.ID] {
			return out, fmt.Errorf("duplicate record %s", r.ID)
		}
		seen[r.ID] = true
	}
	return out, nil
}

func ReadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if e = d.Decode(&struct{}{}); e != io.EOF {
		return c, fmt.Errorf("configuration must contain exactly one JSON document")
	}
	if c.Version != Version {
		return c, fmt.Errorf("unsupported documentation version %d", c.Version)
	}
	return c, nil
}

func MakePlan(inv Inventory, c Config) (Plan, error) {
	if c.Mode != "" && c.Mode != "reference" {
		return Plan{}, fmt.Errorf("unsupported documentation mode %q", c.Mode)
	}
	if c.Mode == "reference" && !c.Catalog {
		return Plan{}, fmt.Errorf("reference mode requires the complete catalog")
	}
	if c.Language != "" && c.Language != "en" && c.Language != "de" {
		return Plan{}, fmt.Errorf("unsupported documentation language %q", c.Language)
	}
	if c.Visibility != "" && c.Visibility != "public" && c.Visibility != "internal" {
		return Plan{}, fmt.Errorf("visibility must be public or internal")
	}
	p := Plan{Version: Version, Clusters: []Cluster{}, Gaps: map[string][]string{}, Unassigned: []string{}}
	records := map[string]Evidence{}
	assigned := map[string]bool{}
	audiences := map[string]bool{}
	for _, r := range inv.Records {
		records[r.ID] = r
		if len(r.Gaps) > 0 {
			p.Gaps[r.ID] = r.Gaps
		}
	}
	for _, a := range c.Audiences {
		if !safeID(a.ID) || a.Title == "" || audiences[a.ID] || (a.Kind != "customer" && a.Kind != "creator") {
			return p, fmt.Errorf("invalid or duplicate audience %q", a.ID)
		}
		audiences[a.ID] = true
		groups := map[string][]string{}
		seen := map[string]bool{}
		for _, id := range a.Features {
			r, ok := records[id]
			if !ok {
				return p, fmt.Errorf("audience %s: unknown record %s", a.ID, id)
			}
			if c.Visibility != "internal" && r.Category == "features" && r.Lifecycle != "active" {
				return p, fmt.Errorf("public audience %s: record %s is not active", a.ID, id)
			}
			if seen[id] {
				return p, fmt.Errorf("duplicate audience record %s", id)
			}
			seen[id] = true
			assigned[id] = true
			groups[r.Topic] = append(groups[r.Topic], id)
		}
		if _, err := dependencyOrder(a.Features, records); err != nil {
			return p, err
		}
		topics := []string{}
		for topic := range groups {
			topics = append(topics, topic)
		}
		sort.Strings(topics)
		for _, topic := range topics {
			ordered, err := dependencyOrder(groups[topic], records)
			if err != nil {
				return p, err
			}
			p.Clusters = append(p.Clusters, Cluster{a.ID, topic, ordered})
		}
	}
	topicIDs := map[string]bool{}
	for _, topic := range c.Topics {
		if !safeID(topic.ID) || topic.Title == "" || topicIDs[topic.ID] {
			return p, fmt.Errorf("invalid or duplicate topic %q", topic.ID)
		}
		topicIDs[topic.ID] = true
	}
	if c.Catalog {
		for _, r := range inv.Records {
			if r.Category == "features" && (c.Visibility == "internal" || r.Lifecycle == "active") {
				assigned[r.ID] = true
			}
		}
	}
	for id := range c.FeatureLabels {
		if _, ok := records[id]; !ok {
			return p, fmt.Errorf("unknown feature label %s", id)
		}
	}
	articleIDs := map[string]bool{}
	for _, a := range c.Articles {
		if !safeID(a.ID) || articleIDs[a.ID] || a.Title == "" || (!audiences[a.Audience] && !(c.Mode == "reference" && a.Audience == "")) || len(a.Features) == 0 || len(a.Sections) == 0 {
			return p, fmt.Errorf("invalid article %q", a.ID)
		}
		articleIDs[a.ID] = true
		switch a.Kind {
		case "getting-started", "guide", "feature", "faq", "overview", "reference", "concept":
		default:
			return p, fmt.Errorf("invalid article kind %q", a.Kind)
		}
		allowed := map[string]bool{}
		for _, id := range a.Features {
			if _, ok := records[id]; !ok {
				return p, fmt.Errorf("article %s: unknown record %s", a.ID, id)
			}
			if c.Visibility != "internal" && records[id].Category == "features" && records[id].Lifecycle != "active" {
				return p, fmt.Errorf("public article %s: source %s is not active", a.ID, id)
			}
			allowed[id] = true
			assigned[id] = true
			found := false
			for _, aud := range c.Audiences {
				if aud.ID == a.Audience {
					for _, fid := range aud.Features {
						if fid == id {
							found = true
						}
					}
				}
			}
			if !found && c.Mode != "reference" {
				return p, fmt.Errorf("article %s: record %s outside audience", a.ID, id)
			}
		}
		if c.Mode == "reference" && len(c.Topics) > 0 && a.Topic == "" {
			return p, fmt.Errorf("article %s: reference article needs a topic", a.ID)
		}
		if a.Topic != "" && a.Topic != "other" && !topicIDs[a.Topic] {
			return p, fmt.Errorf("article %s: unknown topic %s", a.ID, a.Topic)
		}
		for _, note := range a.Notes {
			if !audiences[note.Perspective] || note.Title == "" || strings.TrimSpace(note.Body) == "" || len(note.Sources) == 0 {
				return p, fmt.Errorf("article %s: invalid perspective note", a.ID)
			}
			for _, id := range note.Sources {
				if !allowed[id] {
					return p, fmt.Errorf("article %s: uncited perspective source %s", a.ID, id)
				}
			}
		}
		if a.Diagram != nil {
			if e := validateDiagram(*a.Diagram, allowed); e != nil {
				return p, fmt.Errorf("article %s: %w", a.ID, e)
			}
		}
		for _, s := range a.Sections {
			if s.Title == "" || strings.TrimSpace(s.Body) == "" || len(s.Sources) == 0 {
				return p, fmt.Errorf("article %s: section needs title, prose and sources", a.ID)
			}
			if s.Table != nil {
				if len(s.Table.Columns) == 0 {
					return p, fmt.Errorf("article %s: table needs columns", a.ID)
				}
				for _, row := range s.Table.Rows {
					if len(row) != len(s.Table.Columns) {
						return p, fmt.Errorf("article %s: table row width differs", a.ID)
					}
				}
			}
			for _, id := range s.Sources {
				if !allowed[id] {
					return p, fmt.Errorf("article %s: uncited source %s", a.ID, id)
				}
			}
		}
	}
	if c.Catalog {
		for _, topic := range topicViews(c, featureCatalog(inv, c)) {
			ids := []string{}
			for _, f := range topic.Features {
				ids = append(ids, f.Evidence.ID)
			}
			if len(ids) > 0 {
				p.Clusters = append(p.Clusters, Cluster{Topic: topic.Topic.ID, Features: ids})
			}
		}
	}
	for _, r := range inv.Records {
		if !assigned[r.ID] {
			p.Unassigned = append(p.Unassigned, r.ID)
		}
	}
	return p, nil
}
func safeID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
func dependencyOrder(ids []string, records map[string]Evidence) ([]string, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	state := map[string]int{}
	out := []string{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		r := records[id]
		deps := []string{}
		for _, d := range array(object(r.Fields, "lineage"), "depends_on") {
			if dep, ok := d.(string); ok {
				key := dep
				if !strings.Contains(dep, ":") {
					key = Key(r.Registry, dep)
				}
				if selected[key] {
					deps = append(deps, key)
				}
			}
		}
		sort.Strings(deps)
		for _, d := range deps {
			if e := visit(d); e != nil {
				return e
			}
		}
		state[id] = 2
		out = append(out, id)
		return nil
	}
	for _, id := range ids {
		if e := visit(id); e != nil {
			return nil, e
		}
	}
	return out, nil
}

func validateDiagram(d Diagram, allowed map[string]bool) error {
	if strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Description) == "" || len(d.Nodes) == 0 || len(d.Sources) == 0 {
		return fmt.Errorf("diagram requires title, text equivalent, nodes and sources")
	}
	for _, id := range d.Sources {
		if !allowed[id] {
			return fmt.Errorf("diagram source outside article: %s", id)
		}
	}
	seen := map[string]bool{}
	for _, n := range d.Nodes {
		if !safeID(n.ID) || seen[n.ID] || len([]rune(n.Label)) > 38 || strings.TrimSpace(n.Label) == "" || n.X < 8 || n.X > 480 || n.Y < 8 || n.Y > 1800 {
			return fmt.Errorf("invalid diagram node %s (label <=38 characters, x 8..480, y 8..1800)", n.ID)
		}
		seen[n.ID] = true
	}
	for _, e := range d.Edges {
		if !seen[e.From] || !seen[e.To] || e.From == e.To {
			return fmt.Errorf("invalid diagram edge %s -> %s", e.From, e.To)
		}
	}
	return nil
}
