// Checks variant declarations and lifecycle coverage.
package facet

import (
	"fmt"
	"sort"
)

func sortedVariantNames(m map[string]VariantState) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// lintVariants checks the variant map itself and the records against it.
func lintVariants(reg Registry, rep *Report, features []Record, anchors []Anchor, sm *VariantMap) {
	// An anchored file matching no pattern means the map has rotted. Reported
	// once per file: without this the map silently stops covering new packages
	// and coverage under-reports with nothing turning red.
	seen := map[string]bool{}
	for _, a := range anchors {
		rel := reg.Rel(a.Path)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if _, mapped := sm.VariantOf(rel); !mapped {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "variants",
				Path:     rel,
				Message:  "anchored file belongs to no declared variant and no shared path",
				Hint:     "add a pattern covering it to " + reg.Rel(reg.VariantMapPath()),
			})
		}
	}

	// A 'replaces' pointing at nothing is a typo that would silently drop a
	// migration out of every report built on it.
	for _, name := range sm.Names() {
		target := sm.Replaces(name)
		if target != "" && !sm.Declared(target) {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "variants",
				Path:     reg.Rel(reg.VariantMapPath()),
				Message:  fmt.Sprintf("variant %q declares it replaces %q, which is not declared", name, target),
				Hint:     "correct the name, or declare the variant being replaced",
			})
		}
	}

	byID := AnchorsByID(anchors)
	for _, rec := range features {
		variants := rec.Variants()
		if variants == nil {
			continue
		}
		for _, name := range sortedVariantNames(variants) {
			if !sm.Declared(name) {
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "variants",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("record declares variant %q, which is not in the variant map", name),
					Hint:     "declare it in " + reg.Rel(reg.VariantMapPath()) + ", or correct the name",
				})
			}
		}
		// An anchor on a client the record never mentions is someone
		// implementing the feature without telling the record.
		for _, a := range byID[rec.ID] {
			variant, mapped := sm.VariantOf(reg.Rel(a.Path))
			if !mapped || variant == SharedVariant {
				continue
			}
			if _, declared := variants[variant]; !declared {
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "variants",
					ID:       rec.ID,
					Path:     reg.Rel(a.Path),
					Message:  fmt.Sprintf("%q is anchored on variant %q, which the record does not declare", rec.ID, variant),
					Hint:     "add " + variant + " to the record's variants block, or move the anchor",
				})
			}
		}
	}
}

// ActiveAnywhere reports whether the feature is live on any variant.
func (rec Record) ActiveAnywhere() bool {
	variants := rec.Variants()
	if variants == nil {
		return rec.Lifecycle == "active"
	}
	for _, state := range variants {
		if state.Lifecycle == "active" {
			return true
		}
	}
	return false
}

// activeSomewhere reports whether a record claims to exist in shipped code —
// a top-level 'active', or 'active' on any variant. Checks that only apply to
// shipped behaviour ask this rather than reading Lifecycle directly, which a
// record declaring variants does not have.
func activeSomewhere(rec Record) bool {
	if rec.Variants() != nil {
		return rec.ActiveAnywhere()
	}
	return rec.Lifecycle == "active"
}
