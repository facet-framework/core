// Checks scenario structure, examples and record size.
package facet

import (
	"fmt"
	"strings"
)

// lintSiblingSuffix warns about x and x-<word> in one capability. Review is
// needed because the names can describe different features. A genuine duplicate
// counts twice in calibration; one feature on multiple paths uses variants.
// @facet: registry/layout/doubled-features
// @facet-role: related
func lintSiblingSuffix(reg Registry, rep *Report, features []Record) {
	byCapability := map[string][]string{}
	for _, rec := range features {
		domain, capability, feature, ok := SplitSegments(rec.ID)
		if !ok {
			continue
		}
		key := domain + "/" + capability
		byCapability[key] = append(byCapability[key], feature)
	}

	paths := map[string]string{}
	for _, rec := range features {
		paths[rec.ID] = reg.Rel(rec.Path)
	}

	for _, pair := range SiblingSuffixPairs(byCapability) {
		suffixedID := pair.Capability + "/" + pair.Suffixed
		baseID := pair.Capability + "/" + pair.Base
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "sibling-suffix",
			ID:       suffixedID,
			Path:     paths[suffixedID],
			Message:  fmt.Sprintf("record %q sits beside %q, which is its id without the %q suffix", suffixedID, baseID, pair.Suffix),
			Hint:     "read both summaries: if they describe one feature, that is a variant on one record rather than two records (declare docs/facets/variants.yaml). If they are two features, the shared prefix is a coincidence and there is nothing to do here",
		})
	}
}

// lintNoScenarios warns about anchored records with no contract scenarios.
// The schema permits scenarios: [], so schema validation alone cannot catch
// code with no declared promise. It uses loaded records and anchor counts;
// writing missing scenarios is owner work rather than a build error.
func lintNoScenarios(reg Registry, rep *Report, features []Record, byID map[string][]Anchor) {
	for _, rec := range features {
		if len(byID[rec.ID]) == 0 {
			continue
		}
		if len(rec.ContractScenarios()) > 0 {
			continue
		}
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "no-scenarios",
			ID:       rec.ID,
			Path:     reg.Rel(rec.Path),
			Message:  fmt.Sprintf("record %q is anchored in source but declares no contract.scenarios", rec.ID),
			Hint:     "write at least one scenario under contract.scenarios — an anchored record with none cannot say which promise its code exists for",
		})
	}
}

// lintExamples holds a Scenario Outline to the two things a JSON Schema cannot
// say about one, and to nothing else.
func lintExamples(reg Registry, rep *Report, features []Record) {
	for _, rec := range features {
		for _, o := range rec.Outlines() {
			for i, b := range o.Blocks {
				if len(b.Columns) == 0 {
					// No block of this scenario declares any columns, which the
					// schema already faults. Reporting the rows as well would
					// be two findings for one cause, which reads as two
					// defects — the reason lintSizing leaves the zero-scenario
					// case to lintNoScenarios.
					continue
				}
				for r, row := range b.Rows {
					if len(row) == len(b.Columns) {
						continue
					}
					rep.add(Finding{
						Severity: SeverityError,
						Check:    "examples",
						ID:       rec.ID,
						Path:     reg.Rel(rec.Path),
						Message: fmt.Sprintf("scenario %q, %s, row %d: %d value(s) for %d column(s) (%s)",
							o.Scenario, blockLabel(b, i), r+1, len(row), len(b.Columns), strings.Join(b.Columns, ", ")),
						Hint: examplesRowHint(b),
					})
				}
			}

			declared := o.DeclaredColumns()
			for _, p := range o.Placeholders() {
				if declared[p] {
					continue
				}
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "examples",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("scenario %q writes <%s> in its statement, which no examples column answers", o.Scenario, p),
					Hint:     "add the column, correct the spelling, or reword the sentence — a placeholder no column answers is never substituted, and the table under it fills nothing",
				})
			}
		}
	}
}

// blockLabel names a block the way a reader finds it: by its name where it has
// one, and otherwise by its position. A scenario often has two blocks of the
// same shape, so "row 2" alone would not say which table to open.
func blockLabel(b ExamplesBlock, i int) string {
	if b.Name != "" {
		return fmt.Sprintf("examples block %q", b.Name)
	}
	return fmt.Sprintf("examples block %d", i+1)
}

// examplesRowHint points at the block that actually declares the headings. In
// an inheriting block the columns are not written anywhere near the row that
// disagrees with them, and a hint naming a column set the author cannot see in
// front of them is the kind that sends people looking in the wrong file.
func examplesRowHint(b ExamplesBlock) string {
	if b.Inherited {
		return "every row supplies one value per column, in order — these columns are inherited from the first block that declares them, so correct the row or give this block its own 'columns'"
	}
	return "every row supplies one value per column, in order — add the missing value, drop the extra one, or correct 'columns'"
}

// sizingFloor and sizingCeiling bound the scenario count of a shipped record.
const (
	sizingFloor   = 2
	sizingCeiling = 12
)

// lintSizing reports a shipped record whose scenario count puts it at the wrong
// level of the decomposition.
func lintSizing(reg Registry, rep *Report, features []Record) {
	for _, rec := range features {
		if !activeSomewhere(rec) {
			// Skip sizing warnings for records that are not active anywhere: planned
			// records may be stubs whose contracts have not yet been established.
			continue
		}
		n := len(rec.ContractScenarios())
		switch {
		case n == 0:
			// lintNoScenarios owns the zero case, and says something stronger
			// about it. Two findings for one cause would read as two defects.
		case n < sizingFloor:
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "sizing",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("record %q is active with a single contract scenario", rec.ID),
				Hint:     "gate 3 of the sizing test (FACET §2.1.1) asks for two to nine, including a failure path — a feature with one observable behaviour is usually a scenario of its parent, so either write the scenarios it is missing or fold it into the record above it",
			})
		case n > sizingCeiling:
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "sizing",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("record %q declares %d contract scenarios", rec.ID, n),
				Hint:     "gate 3 of the sizing test (FACET §2.1.1) asks for two to nine — past that the scenarios have usually formed groups that do not reference each other, and those groups are the features; check whether this is a capability rather than a feature",
			})
		}
	}
}
