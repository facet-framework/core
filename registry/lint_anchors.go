// Checks that connect source anchors to registry records.
package facet

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// lintRegistryRefs reports a qualified anchor naming a registry that does not
// exist. Such a tag is invisible to every registry — each one skips it as
// somebody else's — so without this check a typo in the prefix silently
// disconnects a file from the registry entirely.
func lintRegistryRefs(reg Registry, rep *Report, anchors []Anchor, known map[string]bool) {
	seen := map[string]bool{}
	for _, a := range anchors {
		if a.Registry == "" || known[a.Registry] {
			continue
		}
		key := a.Registry + "\x00" + a.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "registry-refs",
			ID:       a.Qualified(),
			Path:     reg.Rel(a.Path),
			Message:  fmt.Sprintf("@facet: tag names registry %q, which is not declared", a.Registry),
			Hint:     "correct the prefix, declare the registry in " + ConfigName + ", or drop the prefix to let the scanning registry own the tag",
		})
	}
}

// lintOrphanIDs reports anchors pointing at records that do not exist.
func lintOrphanIDs(reg Registry, rep *Report, byID map[string][]Anchor, opts LintOptions) {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if isFile(reg.PathForID(id)) {
			continue
		}

		// Code exclusive to this registry implementing a feature this registry
		// has no record for is a defect, whoever else happens to own the id.
		// Only shared code gets the benefit of the doubt, and only because
		// every claimant scans it.
		var exclusive []Anchor
		for _, a := range byID[id] {
			if opts.Shared == nil || !opts.Shared(reg.Rel(a.Path)) {
				exclusive = append(exclusive, a)
			}
		}
		if len(exclusive) == 0 && opts.OwnedElsewhere[id] {
			continue
		}

		locs := byID[id]
		hint := fmt.Sprintf("create %s, or correct the tag", reg.Rel(reg.PathForID(id)))
		if len(exclusive) > 0 {
			locs = exclusive
			if opts.OwnedElsewhere[id] {
				hint = fmt.Sprintf(
					"another registry has this feature, but this code belongs to %q alone — "+
						"create %s to document it here too, or move the code somewhere both registries share",
					registryLabel(reg), reg.Rel(reg.PathForID(id)))
			}
		}
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "orphan-ids",
			ID:       id,
			Path:     reg.Rel(locs[0].Path),
			Message:  fmt.Sprintf("@facet: tag references %q, which has no record", id),
			Hint:     hint,
		})
	}
}

// lintOrphanRecords reports active records with no anchor in source.
func lintOrphanRecords(reg Registry, rep *Report, features []Record, byID map[string][]Anchor, sm *VariantMap, opts LintOptions) {
	for _, rec := range features {
		variants := rec.Variants()

		if variants == nil {
			if rec.Lifecycle != "active" {
				continue
			}
			// Presence in a registry follows the same rule as presence on a
			// variant, one level up: code shared with another registry existing
			// does not mean this product reaches the feature. Without this an
			// id documented in two registries is satisfied for both by a single
			// anchor in a shared helper neither of them may call.
			var exclusive int
			for _, a := range byID[rec.ID] {
				if opts.Shared == nil || !opts.Shared(reg.Rel(a.Path)) {
					exclusive++
				}
			}
			if exclusive > 0 {
				continue
			}
			if len(byID[rec.ID]) > 0 {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "orphan-records",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("active record %q is anchored only in code shared with another registry", rec.ID),
					Hint:     "shared logic existing does not establish that this product reaches the feature — anchor the code belonging to " + registryLabel(reg) + " alone, or set lifecycle: planned",
				})
				continue
			}
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "orphan-records",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("active record %q has no @facet: anchor in source", rec.ID),
				Hint:     "add '@facet: " + rec.ID + "' with '@facet-role: primary' to the implementing file, or set lifecycle: planned",
			})
			continue
		}

		// Per-variant: classify this feature's anchors by the client they belong to.
		exclusive := map[string]int{}
		shared := 0
		for _, a := range byID[rec.ID] {
			variant, mapped := sm.VariantOf(reg.Rel(a.Path))
			switch {
			case !mapped:
				// Reported by lintVariants; not this check's business.
			case variant == SharedVariant:
				shared++
			default:
				exclusive[variant]++
			}
		}

		for _, name := range sortedVariantNames(variants) {
			if variants[name].Lifecycle != "active" {
				continue
			}
			if exclusive[name] > 0 {
				continue
			}
			if shared > 0 {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "orphan-records",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("%q is active on %q but only shared code anchors it", rec.ID, name),
					Hint:     "shared logic existing does not establish that the feature exists on this variant — anchor the code specific to " + name + ", or set its lifecycle to planned",
				})
				continue
			}
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "orphan-records",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("%q is active on %q with no anchor there", rec.ID, name),
				Hint:     "anchor the implementing file under a " + name + " path, or set that variant's lifecycle to planned",
			})
		}
	}
}

// lintAnchorRoles reports an active feature whose anchors never say which one
// is the implementation.
func lintAnchorRoles(reg Registry, rep *Report, features []Record, byID map[string][]Anchor) {
	for _, rec := range features {
		if rec.Lifecycle != "active" {
			continue
		}
		anchors := byID[rec.ID]
		if len(anchors) == 0 || hasPrimary(anchors) {
			continue
		}
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "anchor-roles",
			ID:       rec.ID,
			Path:     reg.Rel(rec.Path),
			Message:  fmt.Sprintf("active record %q has %d anchor(s) but none marked '@facet-role: primary'", rec.ID, len(anchors)),
			Hint:     "mark the implementing file with '@facet-role: primary' — without it nothing can tell the feature's own code from the code that merely supports it",
		})
	}
}

func hasPrimary(anchors []Anchor) bool {
	for _, a := range anchors {
		if a.Role == "primary" {
			return true
		}
	}
	return false
}

// lintAnchorProves reports an `@facet-proves:` entry naming a scenario id
// absent from the anchored record's own contract.scenarios.
func lintAnchorProves(reg Registry, rep *Report, features []Record, byID map[string][]Anchor) {
	for _, rec := range features {
		anchors := byID[rec.ID]
		if len(anchors) == 0 {
			continue
		}
		scenarioIDs := rec.ContractScenarios()
		scenarios := make(map[string]bool, len(scenarioIDs))
		for _, id := range scenarioIDs {
			scenarios[id] = true
		}
		for _, a := range anchors {
			for _, p := range a.Proves {
				if scenarios[p] {
					continue
				}
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "anchor-proves",
					ID:       rec.ID,
					Path:     reg.Rel(a.Path),
					Message:  fmt.Sprintf("@facet-proves: names %q, which is not one of %q's contract.scenarios", p, rec.ID),
					Hint:     "correct the scenario id to match contract.scenarios, or add the scenario if the anchor is right",
				})
			}
		}
	}
}

// lintTestOnlyAnchors warns when an active record's primary anchored identifier
// has no occurrence outside test code. Being anchored and unit-tested does not
// prove that product code calls it. This textual heuristic cannot distinguish
// a call from a comment; an occurrence is not proof of reachability. Supporting
// anchors are excluded, and missing primaries belong to lintAnchorRoles.
func lintTestOnlyAnchors(reg Registry, rep *Report, features []Record, byID map[string][]Anchor, opts LintOptions) {
	type candidate struct {
		rec        Record
		anchor     Anchor
		identifier string
		declLine   int
	}
	type span struct {
		path       string
		start, end int
	}

	fileLines := map[string][]string{}
	idents := map[string]bool{}
	var candidates []candidate
	for _, rec := range features {
		if !rec.ActiveAnywhere() {
			continue
		}
		for _, a := range byID[rec.ID] {
			if a.Role != "primary" {
				continue
			}
			lines, ok := fileLines[a.Path]
			if !ok {
				data, err := os.ReadFile(a.Path)
				if err != nil {
					continue
				}
				lines = strings.Split(string(data), "\n")
				fileLines[a.Path] = lines
			}
			id, declLine, ok := anchoredIdentifier(lines, a.Line)
			if !ok {
				continue
			}
			idents[id] = true
			candidates = append(candidates, candidate{rec: rec, anchor: a, identifier: id, declLine: declLine})
		}
	}
	if len(candidates) == 0 {
		return
	}

	declSpans := map[string][]span{}
	for _, c := range candidates {
		declSpans[c.identifier] = append(declSpans[c.identifier],
			span{path: c.anchor.Path, start: c.anchor.Line, end: c.declLine})
	}
	inDeclSpan := func(identifier, path string, line int) bool {
		for _, s := range declSpans[identifier] {
			if s.path == path && line >= s.start && line <= s.end {
				return true
			}
		}
		return false
	}

	isTest := opts.IsTestPath
	if isTest == nil {
		isTest = IsTestPath
	}
	occ := occurrencesFor(reg.Root, reg.RecordsDir, opts.Scan, idents, isTest)

	reported := map[string]bool{}
	for _, c := range candidates {
		if reported[c.rec.ID] {
			continue
		}
		var nonTest, test bool
		for _, o := range occ[c.identifier] {
			if inDeclSpan(c.identifier, o.Path, o.Line) {
				continue
			}
			if o.Test {
				test = true
			} else {
				nonTest = true
			}
		}
		if nonTest || !test {
			// Reachable outside tests, or no evidence either way — this check
			// only speaks where it has real evidence of a test-only caller.
			continue
		}
		reported[c.rec.ID] = true
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "test-only-anchors",
			ID:       c.rec.ID,
			Path:     reg.Rel(c.rec.Path),
			Message: fmt.Sprintf(
				"active record %q is anchored to %s, which is reachable only from tests",
				c.rec.ID, c.identifier),
			Hint: "wire it into the path the product actually runs, or set lifecycle: planned",
		})
	}
}
