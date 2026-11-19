package facet

import "fmt"

// lintLostAnchors reports an implementation association the working tree lost
// since its baseline: an anchored file deleted, or an anchor removed from a
// file that is still there.
//
// @facet: registry/history/reference-changes
// @facet-role: related
func lintLostAnchors(reg Registry, rep *Report, cs *ChangeSet, current []Anchor, knownIDs map[string]bool) {
	if cs == nil {
		return
	}
	switch {
	case cs.Baseline.Status == BaselineOK:
	case cs.Baseline.Status == BaselineNoRepository:
		// No history, therefore no statement — created-at-history's rule.
		// A registry outside git has no before-state for anything to be lost
		// from, and the commands that compare say so in their own output.
		return
	case cs.Baseline.Status == BaselineMissing && cs.Baseline.Kind == BaselineHead:
		// A repository before its first commit: nothing was ever committed,
		// so nothing can have been lost from it.
		return
	default:
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "lost-anchors",
			Message:  "lost implementation references could not be determined: " + cs.Baseline.Error,
			Hint:     "this is missing evidence, not a clean result — run inside a git checkout with history, or pass --skip lost-anchors",
		})
		return
	}

	known := knownIDs
	if known == nil {
		known = knownIDs1(reg)
	}
	changes := cs.ReferenceChanges(reg, current, known)
	// The lazy pickaxe query can fail after the baseline itself resolved.
	if cs.Baseline.Status != BaselineOK {
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "lost-anchors",
			Message:  "lost implementation references could not be determined: " + cs.Baseline.Error,
			Hint:     "this is missing evidence, not a clean result — run inside a git checkout with history, or pass --skip lost-anchors",
		})
		return
	}
	for _, c := range changes {
		if !c.Lost() {
			continue
		}
		what := "was deleted"
		if c.Kind == RefAnchorRemoved {
			what = "no longer carries its anchor"
		}
		role := ""
		if c.Role != "" {
			role = " " + c.Role
		}
		source := ""
		if c.Evidence == EvidencePrivateSidecar {
			source = " (a private anchor, read from this clone's sidecar)"
		}
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "lost-anchors",
			ID:       c.ID,
			Path:     c.Path,
			Message: fmt.Sprintf("%q lost a%s implementation reference: %s %s%s; %d anchor(s) remain",
				c.ID, role, c.Path, what, source, c.Surviving),
			Hint: "check the surviving implementation, the verification entries that named this code, and the lifecycle; update the record or move the anchor to the code's new home — nothing is retired automatically",
		})
	}
	for _, p := range cs.Unreadable {
		if !reg.Claims(p) {
			continue
		}
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "lost-anchors",
			Path:     p,
			Message:  "the baseline content of " + p + " could not be read, so the references it held are unknown",
			Hint:     "this is missing evidence, not a clean result",
		})
	}
}

// knownIDs1 is the record ids of one registry, for a caller of Lint that did
// not pass the project's.
func knownIDs1(reg Registry) map[string]bool {
	return knownIDs([]Registry{reg})
}
