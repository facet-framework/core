package facet

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Drift is anchored source that changed while the record it anchors did not.
//
// @facet: authoring/guardrails/drift-notice
// @facet-role: related
type Drift struct {
	// ID is the record whose anchored source moved.
	ID string `json:"id"`
	// Path is the changed file, relative to the repository root.
	Path string `json:"path"`
	// ChangedAt is the file's modification time. The change is in the working
	// tree, measured against a baseline rather than a commit of its own, so
	// there is no commit to date it by.
	ChangedAt string `json:"changedAt"`
	// Registry names which of a monorepo's registries reported the entry.
	// Empty in a project declaring none, where the name would be invented.
	Registry string `json:"registry,omitempty"`
}

// ChangedFiles lists tracked and untracked changes relative to the repository
// root, using its resolved baseline. Deleted paths retain their old names.
// It returns no paths when comparison is unavailable; use LoadChangeSet and
// inspect Baseline to distinguish that state from an unchanged working tree.
func ChangedFiles(root string) []string {
	cs := LoadChangeSet(root, "")
	return cs.Changed()
}

// DeletedFiles lists the paths removed since the baseline. A rename's old path
// is not a deletion; RenamedFiles reports those.
func DeletedFiles(root string) []string {
	cs := LoadChangeSet(root, "")
	return cs.Deleted()
}

// RenamedFiles maps the old path to the new one for every rename since the
// baseline.
func RenamedFiles(root string) map[string]string {
	cs := LoadChangeSet(root, "")
	return cs.Renamed()
}

// StaleReference is one record that still names a path a rename has moved.
type StaleReference struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	RenamedTo string `json:"renamedTo"`
	Registry  string `json:"registry,omitempty"`
}

// DriftReport is the one answer every surface gives: what moved since the
// baseline, what the baseline was, and which associations were lost.
type DriftReport struct {
	Repositories []RepositoryBaseline `json:"repositories,omitempty"`
	Baseline     Baseline             `json:"baseline"`
	// Drift is anchored source that changed while its record stood still.
	Drift []Drift `json:"drift"`
	// StaleReferences are records still naming a path a rename moved.
	StaleReferences []StaleReference `json:"staleReferences"`
	// ReferenceChanges are the implementation associations that a deletion,
	// a rename or a removed anchor moved or lost.
	ReferenceChanges []ReferenceChange `json:"referenceChanges"`
	// Unreadable names baseline paths whose previous content could not be
	// read, so whatever they anchored is unknown rather than absent.
	Unreadable []string `json:"unreadable"`
}

// RegistryDrift answers the drift question for one registry over a change set
// already loaded, from anchors the caller already scanned.
func RegistryDrift(cs *ChangeSet, reg Registry, anchors []Anchor, followed, known map[string]bool) ([]Drift, []ReferenceChange) {
	if len(cs.sources) > 0 {
		var drift []Drift
		for _, source := range cs.sources {
			if source.recordsOnly {
				continue
			}
			if !source.changes.OK() && source.changes.Baseline.Status != BaselineMissing {
				continue
			}
			touched := followed
			if source.changes.Baseline.Reviewed() {
				touched = nil
			}
			var changed []string
			for _, file := range source.files {
				changed = append(changed, file.Path)
			}
			drift = append(drift, DriftedAnchors(reg, anchors, changed, touched, known)...)
		}
		for i := range drift {
			drift[i].Registry = reg.Name
		}
		return drift, cs.ReferenceChanges(reg, anchors, known)
	}
	if !cs.OK() && cs.Baseline.Status != BaselineMissing {
		return nil, nil
	}
	drift := DriftedAnchors(reg, anchors, cs.Changed(), followed, known)
	for i := range drift {
		drift[i].Registry = reg.Name
	}
	return drift, cs.ReferenceChanges(reg, anchors, known)
}

// ComputeDrift runs the whole comparison for a project, once: one change set
// against one baseline, handed to every registry's own anchor scan. An empty
// ref means the worktree's accepted baseline, or HEAD when it has none.
func ComputeDrift(root string, registries []Registry, ref string) (DriftReport, error) {
	return ComputeDriftInRepository(root, registries, ref, "")
}

// ComputeDriftInRepository optionally selects one declared source repository.
func ComputeDriftInRepository(root string, registries []Registry, ref, repository string) (DriftReport, error) {
	report := DriftReport{
		Drift:            []Drift{},
		StaleReferences:  []StaleReference{},
		ReferenceChanges: []ReferenceChange{},
		Unreadable:       []string{},
	}
	cs := loadSourceChangeSet(root, registries, ref, repository)
	report.Baseline = cs.Baseline
	report.Repositories = cs.RepositoryBaselines()
	followed := cs.FollowedRecords(registries)
	renames := cs.Renamed()

	for _, reg := range registries {
		scanOpts := ScanOptionsFromEnv()
		if len(reg.Sources) > 0 || reg.SourceView != nil {
			scanOpts = reg.ScanOptions()
		}
		scanned, err := ScanAnchors(reg.Root, scanOpts)
		if err != nil {
			return report, err
		}
		anchors := make([]Anchor, 0, len(scanned))
		for _, a := range scanned {
			if a.BelongsTo(reg.Name) {
				anchors = append(anchors, a)
			}
		}

		// An anchor with no record is the linter's finding, not this one's;
		// knowing which ids this registry answers for lets both checks skip
		// those rather than reporting every orphan as drift.
		known := map[string]bool{}
		for _, cat := range Categories {
			recs, err := reg.LoadCategory(cat)
			if err != nil {
				continue
			}
			for _, rec := range recs {
				known[rec.ID] = true
			}
		}

		drift, refs := RegistryDrift(&cs, reg, anchors, followed, known)
		report.Drift = append(report.Drift, drift...)
		if reg.SourceView != nil {
			for i := range refs {
				refs[i].Path = portableSourcePath(reg, refs[i].Path)
				if refs[i].NewPath != "" {
					refs[i].NewPath = portableSourcePath(reg, refs[i].NewPath)
				}
			}
		}
		report.ReferenceChanges = append(report.ReferenceChanges, refs...)

		for id, oldPaths := range StaleReferences(reg, renames) {
			for _, old := range oldPaths {
				report.StaleReferences = append(report.StaleReferences, StaleReference{
					ID: id, Path: old, RenamedTo: renames[old], Registry: reg.Name,
				})
			}
		}
	}
	report.Unreadable = append(report.Unreadable, cs.Unreadable...)
	// Read again: a git call that failed during the comparison downgrades the
	// status, and the report must say so rather than the status it began with.
	report.Baseline = cs.Baseline
	report.Repositories = cs.RepositoryBaselines()

	sort.Slice(report.Drift, func(i, j int) bool {
		if report.Drift[i].ID != report.Drift[j].ID {
			return report.Drift[i].ID < report.Drift[j].ID
		}
		return report.Drift[i].Path < report.Drift[j].Path
	})
	sort.Slice(report.StaleReferences, func(i, j int) bool {
		if report.StaleReferences[i].ID != report.StaleReferences[j].ID {
			return report.StaleReferences[i].ID < report.StaleReferences[j].ID
		}
		return report.StaleReferences[i].Path < report.StaleReferences[j].Path
	})
	sort.SliceStable(report.ReferenceChanges, func(i, j int) bool {
		a, b := report.ReferenceChanges[i], report.ReferenceChanges[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Registry < b.Registry
	})
	return report, nil
}

// StaleReferences names the records that still mention a path git has recorded
// as renamed. The search is textual on purpose: a path can be named in
// 'verification', in a lineage note, or in prose, and all three go stale the
// same way.
func StaleReferences(reg Registry, renames map[string]string) map[string][]string {
	if len(renames) == 0 {
		return nil
	}
	records, err := reg.LoadFeatures()
	if err != nil {
		return nil
	}
	stale := map[string][]string{}
	for _, rec := range records {
		data, err := os.ReadFile(rec.Path)
		if err != nil {
			continue
		}
		body := string(data)
		for old := range renames {
			if strings.Contains(body, old) {
				stale[rec.ID] = append(stale[rec.ID], old)
			}
		}
		sort.Strings(stale[rec.ID])
	}
	return stale
}

// TouchedRecords collects the feature ids whose record files are among the
// changed paths. A record that moved with its code has already been followed —
// a heuristic that only holds against HEAD; see ChangeSet.FollowedRecords.
func TouchedRecords(registries []Registry, changed []string) map[string]bool {
	touched := map[string]bool{}
	for _, reg := range registries {
		prefix := reg.Rel(reg.RecordsDir) + "/features/"
		for _, rel := range changed {
			if strings.HasPrefix(rel, prefix) {
				touched[strings.TrimSuffix(strings.TrimPrefix(rel, prefix), ".yaml")] = true
			}
		}
	}
	return touched
}

// DriftedAnchors reports which anchors sit in source that changed without their
// record following.
func DriftedAnchors(reg Registry, anchors []Anchor, changed []string, touched map[string]bool, known map[string]bool) []Drift {
	if len(changed) == 0 || len(anchors) == 0 {
		return nil
	}
	changedSet := make(map[string]bool, len(changed))
	for _, rel := range changed {
		changedSet[filepath.ToSlash(rel)] = true
	}
	recordsPrefix := reg.Rel(reg.RecordsDir) + "/"

	seen := map[string]bool{}
	var out []Drift
	for _, a := range anchors {
		rel := reg.Rel(a.Path)
		if !changedSet[rel] || strings.HasPrefix(rel, recordsPrefix) {
			continue
		}
		if touched[a.ID] || !known[a.ID] {
			continue
		}
		key := a.ID + "\x00" + rel
		if seen[key] {
			continue
		}
		seen[key] = true
		if a.Reference != "" {
			rel = a.Reference
		}
		out = append(out, Drift{ID: a.ID, Path: rel, ChangedAt: modifiedAt(a.Path)})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// modifiedAt dates a working-tree change. An unreadable file yields an empty
// string rather than a made-up time.
func modifiedAt(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
