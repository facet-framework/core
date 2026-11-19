// Checks feature flags, knowledge, backing and deviations.
package facet

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// lintFlags checks that declared feature flags still exist in source, and that
// none has outlived its retirement date.
//
// @facet: registry/integrity/flag-retirement
// @facet-role: primary
func lintFlags(reg Registry, rep *Report, features []Record, opts LintOptions) {
	var keys []string
	seen := map[string]bool{}
	for _, rec := range features {
		for _, f := range rec.Flags() {
			if !seen[f.Key] {
				seen[f.Key] = true
				keys = append(keys, f.Key)
			}
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	found, err := ScanLiterals(reg.Root, opts.Scan, keys, reg.RecordsDir+string(filepath.Separator))
	if err != nil {
		return
	}

	for _, rec := range features {
		for _, f := range rec.Flags() {
			if !found[f.Key] {
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "flags",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("flag key %q appears nowhere in source", f.Key),
					Hint:     "the gate was removed and the record was not told — the feature is now unconditionally on or off, and the record should say which",
				})
			}
			if f.RetireBy != "" && opts.Today != "" && f.RetireBy < opts.Today {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "flags",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("flag %q was due for retirement on %s", f.Key, f.RetireBy),
					Hint:     "remove the flag and the branch it guards, or agree a new date",
				})
			}
		}
	}
}

// lintKnowledge reports records whose id contradicts their path, and Knowledge
// slots still waiting on their owner.
//
// @facet: registry/integrity/knowledge-gaps
// @facet-role: related
func lintKnowledge(reg Registry, rep *Report, features []Record) {
	for _, rec := range features {
		if expected := reg.IDForPath(rec.Path); expected != "" && rec.ID != "" && rec.ID != expected {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "id-path",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("record declares id %q but its path implies %q", rec.ID, expected),
				Hint:     "rename the file to match the id, or correct the id",
			})
		}
		// Judged on every record, not only an active one: the combination is
		// incoherent whatever the lifecycle, and a planned record carrying it
		// becomes an active one without the contradiction ever being read.
		if bad := rec.KnowledgeContradictions(); len(bad) > 0 {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "knowledge",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("Knowledge slot(s) both answering and declaring no answer: %s", strings.Join(bad, ", ")),
				Hint:     "a slot declaring 'not_applicable' must carry no 'answer' and no 'derive_from' — remove whichever of the two is wrong; the schema refuses this too, and this check speaks when the schema did not run",
			})
		}
		if !rec.ActiveAnywhere() {
			continue
		}
		// Deliberately per slot, never per slot per variant: a project with
		// three variants and four unanswered slots would otherwise produce
		// twelve warnings on one record, and a signal that loud is unread.

		if gaps := rec.KnowledgeGaps(); len(gaps) > 0 {
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "knowledge",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("Knowledge slots awaiting owner input: %s", strings.Join(gaps, ", ")),
				Hint:     "these answers are owner-supplied; leave them empty rather than guessing, and ask the owner — only they can answer the question or rule it out for this feature",
			})
		}
	}
}

// lintBacking checks the declared unbacked scenarios against the rest of the
// registry.
func lintBacking(reg Registry, rep *Report, features []Record, sm *VariantMap) {
	activeAnywhere := make(map[string]bool, len(features))
	for _, rec := range features {
		if rec.ActiveAnywhere() {
			activeAnywhere[rec.ID] = true
		}
	}

	for _, rec := range features {
		for _, b := range rec.Backings() {
			where := b.Scenario
			if b.Variant != "" {
				where = b.Scenario + " on " + b.Variant
			}

			if b.Variant != "" && sm.Configured() && !sm.Declared(b.Variant) {
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "backing",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("scenario %q declares backing for variant %q, which is not in the variant map", b.Scenario, b.Variant),
					Hint:     "declare it in " + reg.Rel(reg.VariantMapPath()) + ", or correct the name — an override nothing reads leaves the scenario looking backed",
				})
			}

			if b.State != Unbacked {
				continue
			}

			// The counterpart of the flag retirement warning: the thing that was
			// going to supply the data has shipped, and the record was not told.
			// Only an id can be checked — a store name or an issue reference is a
			// legitimate answer that the registry cannot resolve.
			if featureIDPattern.MatchString(b.UnblockedBy) && activeAnywhere[b.UnblockedBy] {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "backing",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("scenario %q is unbacked pending %q, which is now active", where, b.UnblockedBy),
					Hint:     "the data may exist now — back the scenario and drop the block, or record what is still missing",
				})
			}
		}
	}
}

// lintDeviation checks the declared deviations against the rest of the
// registry.
func lintDeviation(reg Registry, rep *Report, features []Record) {
	activeAnywhere := make(map[string]bool, len(features))
	for _, rec := range features {
		if rec.ActiveAnywhere() {
			activeAnywhere[rec.ID] = true
		}
	}

	for _, rec := range features {
		for _, d := range rec.Deviations() {
			// Only an id can be checked. A ticket reference is the common
			// answer and a legitimate one the registry cannot resolve, so it is
			// never reported as dangling — faulting it would push authors into
			// the vaguest phrasing that avoids the noise.

			if !featureIDPattern.MatchString(d.TrackedAs) || !activeAnywhere[d.TrackedAs] {
				continue
			}
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "deviation",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("scenario %q declares a deviation tracked as %q, which is now active", d.Scenario, d.TrackedAs),
				Hint:     "the defect may be fixed — check the behaviour, then drop the deviation and state the scenario as the contract it has become, or record what is still wrong",
			})
		}
	}
}
