// Checks record dependencies and supersession.
package facet

import (
	"fmt"
	"strings"
)

// lintDependencies reports a lineage reference that names a record the project
// does not have.
func lintDependencies(reg Registry, rep *Report, known map[string]bool) {
	if known == nil {
		known = knownIDs([]Registry{reg})
	}
	for _, cat := range Categories {
		recs, err := reg.LoadCategory(cat)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			for _, field := range []string{"depends_on", "depended_on_by", "superseded_by"} {
				for _, id := range lineageIDs(rec, field) {
					if known[id] {
						continue
					}
					rep.add(Finding{
						Severity: SeverityError,
						Check:    "dependencies",
						ID:       rec.ID,
						Path:     reg.Rel(rec.Path),
						Message:  fmt.Sprintf("lineage.%s names %q, which no record in this project has", field, id),
						Hint:     "correct the id, write the record it names, or drop the reference — a dependency on nothing is not a dependency",
					})
				}
			}
		}
	}
}

// lineageIDs reads one field out of a record's lineage facet as a list of
// ids. Most such fields (`depends_on`, `depended_on_by`) are already lists;
// `superseded_by` is single-valued, so a bare string is wrapped into a
// one-element list rather than requiring a second reader for the same check.
func lineageIDs(rec Record, field string) []string {
	lineage, ok := rec.Fields["lineage"].(map[string]any)
	if !ok {
		return nil
	}
	switch raw := lineage[field].(type) {
	case []any:
		var out []string
		for _, item := range raw {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if raw == "" {
			return nil
		}
		return []string{raw}
	default:
		return nil
	}
}

// supersededBy reads a record's lineage.superseded_by, or "" if absent.
func supersededBy(rec Record) string {
	lineage, ok := rec.Fields["lineage"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := lineage["superseded_by"].(string)
	return s
}

// lintSupersededBy warns when a resolving superseded_by reference coexists with
// active lifecycle, or when an active successor leaves the predecessor anchored.
// Removing replaced code can be a legitimate transition, so it does not block
// the build. Dangling successor references belong to lintDependencies.
func lintSupersededBy(reg Registry, rep *Report, features []Record, byID map[string][]Anchor) {
	activeAnywhere := make(map[string]bool, len(features))
	for _, rec := range features {
		if rec.ActiveAnywhere() {
			activeAnywhere[rec.ID] = true
		}
	}

	for _, rec := range features {
		successor := supersededBy(rec)
		if successor == "" {
			continue
		}

		if rec.Lifecycle == "active" {
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "superseded-by",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("record declares it was superseded by %q but is still active", successor),
				Hint:     "set lifecycle: deprecated, or drop the reference — a record cannot be both the current implementation and the replaced one",
			})
		}

		if !activeAnywhere[successor] {
			continue
		}
		paths := AnchorPaths(byID[rec.ID])
		if len(paths) == 0 {
			continue
		}
		rel := make([]string, len(paths))
		for i, p := range paths {
			rel[i] = reg.Rel(p)
		}
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "superseded-by",
			ID:       rec.ID,
			Path:     reg.Rel(rec.Path),
			Message: fmt.Sprintf("superseded by %q, which is active, but still anchored in %d file(s)",
				successor, len(paths)),
			Hint: "move the implementation out, or reconsider whether it is really superseded — anchored in " + strings.Join(rel, ", "),
		})
	}
}
