// @facet: registry/history/defect-ledger
// @facet-role: primary
package facet

import (
	"sort"
	"strings"
)

// How an entry came to be in the ledger.
const (
	RecordedByGitHistory = "git-history"
	RecordedByTracker    = "tracker"
	RecordedByManual     = "manual"
)

// Fix is one bugfix commit attributed to one feature.
type Fix struct {
	// Feature is the record's id.
	Feature string `json:"feature"`
	// Path is the record's location, for reporting.
	Path string `json:"path,omitempty"`

	Commit string `json:"commit"`
	Date   string `json:"date"`
	// Files are the repository-relative paths the commit changed that carry an
	// anchor for this feature — not every path the commit touched.
	Files      []string `json:"files"`
	Subject    string   `json:"subject,omitempty"`
	Issue      string   `json:"issue,omitempty"`
	RecordedBy string   `json:"recorded_by,omitempty"`
}

// Exclusion is a commit a reviewer judged not to be a defect in this feature.
type Exclusion struct {
	Feature string `json:"feature"`
	Path    string `json:"path,omitempty"`

	Commit string `json:"commit"`
	Reason string `json:"reason"`
}

// FileCount is one file and how many recorded fixes touched it.
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Hotspot is one feature's whole defect history, reduced to what a reader
// ranking features by fragility needs.
type Hotspot struct {
	Feature string      `json:"feature"`
	Path    string      `json:"path,omitempty"`
	Total   int         `json:"total"`
	LastFix string      `json:"last_fix,omitempty"`
	Files   []FileCount `json:"files,omitempty"`
}

// Fixes returns the record's defect ledger, most recent first.
func (r Record) Fixes() []Fix {
	defects, ok := r.Fields["defects"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := defects["fixes"].([]any)
	if !ok {
		return nil
	}

	var out []Fix
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		f := Fix{Feature: r.ID, Path: r.Path}
		f.Commit, _ = entry["commit"].(string)
		f.Date, _ = entry["date"].(string)
		f.Subject, _ = entry["subject"].(string)
		f.Issue, _ = entry["issue"].(string)
		f.RecordedBy, _ = entry["recorded_by"].(string)
		if files, ok := entry["files"].([]any); ok {
			for _, p := range files {
				if s, ok := p.(string); ok && s != "" {
					f.Files = append(f.Files, s)
				}
			}
		}
		if f.Commit == "" {
			continue
		}
		out = append(out, f)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].Commit < out[j].Commit
	})
	return out
}

// ExcludedCommits returns the commits deliberately not counted, with their
// reasons. Sorted by hash so output is stable.
func (r Record) ExcludedCommits() []Exclusion {
	defects, ok := r.Fields["defects"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := defects["excluded"].([]any)
	if !ok {
		return nil
	}

	var out []Exclusion
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		e := Exclusion{Feature: r.ID, Path: r.Path}
		e.Commit, _ = entry["commit"].(string)
		e.Reason, _ = entry["reason"].(string)
		if e.Commit == "" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Commit < out[j].Commit })
	return out
}

// DefectTotal is the number of recorded fixes.
func (r Record) DefectTotal() int { return len(r.Fixes()) }

// DefectsByFile counts how many recorded fixes touched each file, hottest
// first, ties broken by path so the order is stable.
func (r Record) DefectsByFile() []FileCount {
	counts := map[string]int{}
	for _, f := range r.Fixes() {
		for _, p := range f.Files {
			counts[p]++
		}
	}
	out := make([]FileCount, 0, len(counts))
	for p, n := range counts {
		out = append(out, FileCount{Path: p, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// KnowsCommit reports whether this record has already judged a commit — either
// by recording it as a fix or by excluding it.
func (r Record) KnowsCommit(sha string) bool {
	if sha == "" {
		return false
	}
	sha = strings.ToLower(sha)
	for _, f := range r.Fixes() {
		if shaMatches(f.Commit, sha) {
			return true
		}
	}
	for _, e := range r.ExcludedCommits() {
		if shaMatches(e.Commit, sha) {
			return true
		}
	}
	return false
}

func shaMatches(recorded, candidate string) bool {
	recorded = strings.ToLower(recorded)
	if recorded == "" {
		return false
	}
	return strings.HasPrefix(recorded, candidate) || strings.HasPrefix(candidate, recorded)
}

// Hotspots ranks the records that carry a defect ledger, most fixes first.
func Hotspots(records []Record) []Hotspot {
	var out []Hotspot
	for _, rec := range records {
		fixes := rec.Fixes()
		if len(fixes) == 0 {
			continue
		}
		h := Hotspot{
			Feature: rec.ID,
			Path:    rec.Path,
			Total:   len(fixes),
			LastFix: fixes[0].Date,
			Files:   rec.DefectsByFile(),
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Feature < out[j].Feature
	})
	return out
}

// AllFixes flattens every recorded fix across the registry, newest first.
func AllFixes(records []Record) []Fix {
	var out []Fix
	for _, rec := range records {
		out = append(out, rec.Fixes()...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		if out[i].Commit != out[j].Commit {
			return out[i].Commit < out[j].Commit
		}
		return out[i].Feature < out[j].Feature
	})
	return out
}
