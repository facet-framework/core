package facet

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// SplitLoss is one thing that stood in the parent record and is in none of the
// children.
//
// @facet: registry/layout/recut-loss-detector
// @facet-role: related
type SplitLoss struct {
	Facet  string `json:"facet"`
	Detail string `json:"detail,omitempty"`
}

func (l SplitLoss) String() string {
	if l.Detail == "" {
		return l.Facet
	}
	return l.Facet + ": " + l.Detail
}

// SplitCheck is what one comparison found.
type SplitCheck struct {
	Parent   string      `json:"parent"`
	Children []string    `json:"children"`
	Losses   []SplitLoss `json:"losses"`
}

// splitExempt names the facets that live in exactly one child by design, so
// finding them in only one place is not a loss. Everything outside this set is
// expected in every child that needs it, and absent from all of them is what
// the detector reports.
var splitExempt = map[string]bool{
	"id":             true,
	"schema_version": true,
	"metadata":       true,
	"authorship":     true,
	"lifecycle":      true,
	"contract":       true,
	"intent":         true,
}

// CheckSplit reports what the parent record carried and no child does.
//
// @facet: registry/layout/recut-loss-detector
// @facet-role: primary
func CheckSplit(parentYAML []byte, childrenYAML map[string][]byte) (SplitCheck, error) {
	var parent map[string]any
	if err := yaml.Unmarshal(parentYAML, &parent); err != nil {
		return SplitCheck{}, fmt.Errorf("parsing the parent record: %w", err)
	}

	children := make([]map[string]any, 0, len(childrenYAML))
	names := make([]string, 0, len(childrenYAML))
	for name := range childrenYAML {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var c map[string]any
		if err := yaml.Unmarshal(childrenYAML[name], &c); err != nil {
			return SplitCheck{}, fmt.Errorf("parsing %s: %w", name, err)
		}
		children = append(children, c)
	}

	check := SplitCheck{Children: names}
	if id, ok := parent["id"].(string); ok {
		check.Parent = id
	}

	facets := make([]string, 0, len(parent))
	for k := range parent {
		facets = append(facets, k)
	}
	sort.Strings(facets)

	for _, facet := range facets {
		if splitExempt[facet] {
			continue
		}
		if isEmpty(parent[facet]) {
			continue
		}

		// Present anywhere at all is the first question. A facet in no child is
		// a loss whatever its shape.
		found := false
		for _, c := range children {
			if !isEmpty(c[facet]) {
				found = true
				break
			}
		}
		if !found {
			check.Losses = append(check.Losses, SplitLoss{Facet: facet})
			continue
		}

		// Present somewhere, so the name survived. Whether its contents did is
		// a different question, and for these facets it is the one that matters.
		check.Losses = append(check.Losses, deepLosses(facet, parent[facet], children)...)
	}

	return check, nil
}

var deepFields = map[string][]string{
	"lineage":     {"depends_on", "depended_on_by", "related_assumptions", "supersedes", "superseded_by"},
	"observation": {"slos", "signals"},
	"defects":     {"fixes"},
	"flags":       {"flags"},
}

func deepLosses(facet string, parentValue any, children []map[string]any) []SplitLoss {
	paths, ok := deepFields[facet]
	if !ok {
		return nil
	}
	pm, ok := parentValue.(map[string]any)
	if !ok {
		return nil
	}

	var losses []SplitLoss
	for _, path := range paths {
		want := entryKeys(pm[path])
		if len(want) == 0 {
			continue
		}
		have := map[string]bool{}
		for _, c := range children {
			cm, ok := c[facet].(map[string]any)
			if !ok {
				continue
			}
			for _, k := range entryKeys(cm[path]) {
				have[k] = true
			}
		}
		var missing []string
		for _, k := range want {
			if !have[k] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			losses = append(losses, SplitLoss{
				Facet:  facet,
				Detail: fmt.Sprintf("%s — %s", path, strings.Join(missing, ", ")),
			})
		}
	}
	return losses
}

// entryKeys reduces a list to the identities its entries are compared by. A
// list of strings is its own key; a list of objects is keyed by the first field
// that names it, because an edge rewritten with a new description is still the
// same edge and a report that said otherwise would cry at every split.
func entryKeys(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, item := range list {
		switch e := item.(type) {
		case string:
			keys = append(keys, e)
		case map[string]any:
			for _, field := range []string{"id", "name", "target", "record", "file", "statement"} {
				if s, ok := e[field].(string); ok && s != "" {
					keys = append(keys, s)
					break
				}
			}
		}
	}
	return keys
}

func isEmpty(v any) bool {
	switch e := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(e) == ""
	case []any:
		return len(e) == 0
	case map[string]any:
		if len(e) == 0 {
			return true
		}
		for _, sub := range e {
			if !isEmpty(sub) {
				return false
			}
		}
		return true
	}
	return false
}

// ReadRecordFile is a thin helper so the command can read a path the same way
// the tests hand bytes in.
func ReadRecordFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
