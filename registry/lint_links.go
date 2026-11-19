// Checks references from records to repository documents.
package facet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// lintBrokenLinks reports a repository-relative reference in
// intent.decision_records or observation.alerts[].runbook that names no file
// in the working tree.
//
// @facet: registry/integrity/broken-links
// @facet-role: primary
func lintBrokenLinks(reg Registry, rep *Report) {
	for _, cat := range Categories {
		recs, err := reg.LoadCategory(cat)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			for _, ref := range decisionRecordRefs(rec) {
				checkRepoLink(reg, rep, rec, "intent.decision_records", ref)
			}
			if cat != "features" {
				continue
			}
			for i, runbook := range runbookRefs(rec) {
				if runbook == "" {
					continue
				}
				checkRepoLink(reg, rep, rec, fmt.Sprintf("observation.alerts[%d].runbook", i), runbook)
			}
		}
	}
}

// decisionRecordRefs reads a record's intent.decision_records list. The field
// is identical across the categories that declare an intent facet (features
// and policies today — see schema/facet.schema.json and
// schema/policy.schema.json), so this reads it generically off Fields rather
// than assuming the record is a feature.
func decisionRecordRefs(rec Record) []string {
	intent, ok := rec.Fields["intent"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := intent["decision_records"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// runbookRefs reads observation.alerts[].runbook in declaration order, one
// entry per alert — including "" for an alert with no runbook set — so a
// caller's index into the result always matches the alert's position in the
// YAML.
func runbookRefs(rec Record) []string {
	observation, ok := rec.Fields["observation"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := observation["alerts"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		alert, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out[i], _ = alert["runbook"].(string)
	}
	return out
}

// checkRepoLink reports ref as a broken link when it names a repository-
// relative path with nothing at it. A value that looks like a URL is skipped
// entirely — see lintBrokenLinks.
func checkRepoLink(reg Registry, rep *Report, rec Record, field, ref string) {
	if isURL(ref) {
		return
	}
	// A fragment addresses a heading inside the target, not a file of its
	// own; the existence check is against the path alone, but the finding
	// keeps the fragment so a reader can find the exact reference in the
	// record.
	target := strings.TrimPrefix(strings.SplitN(ref, "#", 2)[0], "/")
	if target == "" {
		return
	}
	full := absUnder(reg.Root, filepath.FromSlash(target))
	if isFile(full) || isDir(full) {
		return
	}
	rep.add(Finding{
		Severity: SeverityWarning,
		Check:    "broken-links",
		ID:       rec.ID,
		Path:     reg.Rel(rec.Path),
		Message:  fmt.Sprintf("%s references %q, which does not exist in the repository", field, ref),
		Hint:     "fix the path, or drop the reference — the record's reasoning lives only where this points",
	})
}

// isURL reports whether a decision_records or runbook value is a URL rather
// than a repository-relative path. Checking only the two schemes §11.2's own
// example uses keeps this from swallowing a path that merely starts with
// something scheme-shaped.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
