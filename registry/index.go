package facet

import (
	"fmt"
	"sort"
	"strings"
)

// IndexOptions bounds what RecordIndex renders.
//
// @facet: navigation/interfaces/record-roster
// @facet-role: primary
type IndexOptions struct {
	// MaxBytes bounds the rendered index. Zero uses DefaultIndexBytes.
	MaxBytes int
	// Summaries includes the first sentence of each record's intent. Off gives
	// the bare roster, which is what fits when a registry is very large.
	Summaries bool
}

// DefaultIndexBytes is the budget one index gets, in bytes.
const DefaultIndexBytes = 24 * 1024

// IndexEntry is one record, reduced to what routing needs.
type IndexEntry struct {
	Registry  string
	ID        string
	Lifecycle string
	Team      string
	Summary   string
}

// BuildIndex reads the registries and reduces each record to an entry.
func BuildIndex(regs []Registry) []IndexEntry {
	var out []IndexEntry
	for _, reg := range regs {
		features, err := reg.LoadFeatures()
		if err != nil {
			continue
		}
		for _, rec := range features {
			if rec.ID == "" {
				continue
			}
			out = append(out, IndexEntry{
				Registry:  reg.Name,
				ID:        rec.ID,
				Lifecycle: indexLifecycle(rec),
				Team:      recordTeam(rec),
				Summary:   firstSentence(rec.Intent()),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Registry != out[j].Registry {
			return out[i].Registry < out[j].Registry
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RecordIndex renders the index as the text an agent is given.
func RecordIndex(regs []Registry, opts IndexOptions) string {
	entries := BuildIndex(regs)
	if len(entries) == 0 {
		return ""
	}
	budget := opts.MaxBytes
	if budget <= 0 {
		budget = DefaultIndexBytes
	}

	// Summaries first, then the tail. Both attempts are rendered rather than
	// estimated, because the header and the per-registry lines vary with the
	// project and a guess at their size would either overshoot the budget or
	// waste it.
	if text := renderIndex(regs, entries, opts.Summaries, len(entries)); len(text) <= budget {
		return text
	}
	if opts.Summaries {
		if text := renderIndex(regs, entries, false, len(entries)); len(text) <= budget {
			return text
		}
	}
	keep := sort.Search(len(entries), func(i int) bool {
		return len(renderIndex(regs, entries, false, i+1)) > budget
	})
	if keep == 0 {
		return ""
	}
	return renderIndex(regs, entries, false, keep)
}

// renderIndex writes the first keep entries, noting what it left out.
func renderIndex(regs []Registry, entries []IndexEntry, summaries bool, keep int) string {
	var b strings.Builder
	multi := len(regs) > 1

	fmt.Fprintf(&b, "FACET record index — all %d record(s) in this project, so you do not have to search for them.\n", len(entries))
	if !multi && len(regs) == 1 {
		// Label output paths explicitly as repository-relative so callers resolve
		// them against the project root.
		fmt.Fprintf(&b, "A record's file is %s/features/<id>.yaml — for example %s/features/%s.yaml.\nThese paths are relative to the repository root. Never prefix one with '/'.\n",
			relDir(regs[0]), relDir(regs[0]), entries[0].ID)
	}
	b.WriteString("Read the one line that matches the question, then open that record. Only search when nothing here fits.\n")

	current := "\x00"
	for i := 0; i < keep && i < len(entries); i++ {
		entry := entries[i]
		if multi && entry.Registry != current {
			current = entry.Registry
			name := entry.Registry
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Fprintf(&b, "\nRegistry %s — %s/features/<id>.yaml:\n", name, relDirNamed(regs, entry.Registry))
		} else if i == 0 {
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "- %s [%s]", entry.ID, entry.Lifecycle)
		if entry.Team != "" {
			fmt.Fprintf(&b, " (%s)", entry.Team)
		}
		if summaries && entry.Summary != "" {
			fmt.Fprintf(&b, " — %s", entry.Summary)
		}
		b.WriteString("\n")
	}

	if keep < len(entries) {
		fmt.Fprintf(&b, "\n%d further record(s) are not listed here: the index did not fit. Search the records directory for those.\n",
			len(entries)-keep)
	}
	return b.String()
}

func relDir(reg Registry) string {
	rel := reg.Rel(reg.RecordsDir)
	if rel == "" {
		return reg.RecordsDir
	}
	return rel
}

func relDirNamed(regs []Registry, name string) string {
	for _, reg := range regs {
		if reg.Name == name {
			return relDir(reg)
		}
	}
	return "the records directory"
}

// indexLifecycle reports one word for a record's state.
func indexLifecycle(rec Record) string {
	if variants := rec.Variants(); variants != nil {
		if rec.ActiveAnywhere() {
			return "active"
		}
		return "per-variant"
	}
	if rec.Lifecycle == "" {
		return "unknown"
	}
	return rec.Lifecycle
}

// recordTeam reports the owning team, which is how a question about a domain
// finds its records when it names no id at all.
func recordTeam(rec Record) string {
	lineage, ok := rec.Fields["lineage"].(map[string]any)
	if !ok {
		return ""
	}
	owner, ok := lineage["owner"].(map[string]any)
	if !ok {
		return ""
	}
	team, _ := owner["team"].(string)
	return team
}

// firstSentence reduces an intent summary to its opening sentence.
func firstSentence(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		return ""
	}
	// Abbreviations are not sentence ends. Only a full stop followed by a space
	// and a capital counts, which leaves "e.g. the list" alone.
	for i := 0; i < len(flat)-2; i++ {
		if flat[i] != '.' && flat[i] != '!' && flat[i] != '?' {
			continue
		}
		if flat[i+1] != ' ' {
			continue
		}
		next := flat[i+2]
		if next >= 'A' && next <= 'Z' {
			return flat[:i+1]
		}
	}
	const limit = 220
	if len(flat) > limit {
		return strings.TrimSpace(flat[:limit]) + "…"
	}
	return flat
}
