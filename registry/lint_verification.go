package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// lintProves checks record-local scenario references in verification entries.
// Unknown scenarios are errors. Scenarios without a matching reference produce
// one sorted warning per record. The association does not prove a test passed.
//
// @facet: registry/integrity/proof-references
// @facet-role: primary
func lintProves(reg Registry, rep *Report, features []Record) {
	for _, rec := range features {
		scenarioIDs := rec.ContractScenarios()
		if len(scenarioIDs) == 0 {
			continue
		}
		scenarios := make(map[string]bool, len(scenarioIDs))
		for _, id := range scenarioIDs {
			scenarios[id] = true
		}

		proven := make(map[string]bool)
		for _, p := range rec.Provenances() {
			if !scenarios[p.Scenario] {
				rep.add(Finding{
					Severity: SeverityError,
					Check:    "proves",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("verification.%s %q proves %q, which is not one of this record's contract.scenarios", p.Kind, p.TestID, p.Scenario),
					Hint:     "correct the scenario id to match contract.scenarios, or update contract.scenarios if the scenario was renamed",
				})
				continue
			}
			proven[p.Scenario] = true
		}

		var unproven []string
		for _, id := range scenarioIDs {
			if !proven[id] {
				unproven = append(unproven, id)
			}
		}
		if len(unproven) > 0 {
			sort.Strings(unproven)
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "proves",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("contract scenario(s) with no verification proof: %s", strings.Join(unproven, ", ")),
				Hint:     "add a verification entry whose 'proves' names the scenario, or leave it unproven for now — this is a coverage signal, not a defect",
			})
		}
	}
}

// verificationPath extracts a repository path from a verification test ID.
// It strips ::test and /file:job suffixes, then requires a slash and no whitespace
// in the remaining path. Prose identifiers and bare root filenames are ignored.
// Both target-existence and unclaimed-test checks use this interpretation.
func verificationPath(testID string) (string, bool) {
	head := testID
	if i := strings.Index(head, "::"); i >= 0 {
		head = head[:i]
	}
	if slash := strings.LastIndex(head, "/"); slash >= 0 {
		if colon := strings.Index(head[slash:], ":"); colon >= 0 {
			head = head[:slash+colon]
		}
	}

	if head == "" || strings.ContainsAny(head, " \t") || !strings.Contains(head, "/") {
		return "", false
	}
	return head, true
}

// findUninitializedSubmodules is also used by single-record validation. Tests
// replace it to verify that an existing target never triggers an index scan.
var findUninitializedSubmodules = uninitializedSubmodules

// lintVerificationTargets checks that path-shaped test IDs exist in the project.
// A missing target inside an uninitialized submodule warns because its checkout
// is unavailable; other missing targets are errors. Paths use the registry root.
//
// @facet: registry/integrity/proof-references
// @facet-role: related
func lintVerificationTargets(reg Registry, rep *Report, features []Record) {
	var uninitialized []string
	checkedSubmodules := false
	for _, rec := range features {
		// One finding per missing file, not per scenario: a single test file
		// named for many scenarios would otherwise produce one identical line
		// per scenario, and that is a signal nobody reads to the end.
		seen := make(map[string]bool)
		for _, p := range rec.Provenances() {
			rel, ok := verificationPath(p.TestID)
			if !ok || seen[p.TestID] {
				continue
			}
			seen[p.TestID] = true
			if reg.SourceView != nil {
				if _, err := selectedTestReference(reg, rel); err != nil {
					rep.add(Finding{Severity: SeverityError, Check: "verification-targets", ID: rec.ID, Path: reg.Rel(rec.Path), Message: fmt.Sprintf("verification.%s names %q: %v", p.Kind, p.TestID, err)})
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(reg.Root, filepath.FromSlash(rel))); err == nil {
				continue
			}
			// Reading Git's full index costs work proportional to the checkout and
			// is needed only to refine an already-missing target into the special
			// uninitialised-submodule warning.
			if !checkedSubmodules {
				uninitialized = findUninitializedSubmodules(reg.Root)
				checkedSubmodules = true
			}
			if module := containingSubmodule(reg.Root, rel, uninitialized); module != "" {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "verification-targets",
					ID:       rec.ID,
					Path:     reg.Rel(rec.Path),
					Message:  fmt.Sprintf("verification.%s names %q inside uninitialized submodule %q; the file cannot be checked", p.Kind, p.TestID, module),
					Hint:     fmt.Sprintf("run git submodule update --init -- %s, then rerun facet lint", shellQuote(module)),
				})
				continue
			}
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "verification-targets",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("verification.%s names %q, which is not a file in this project", p.Kind, p.TestID),
				Hint:     "correct the path — a moved test keeps proving what it proved, but only once the record says where it went; delete the entry if the test is gone",
			})
		}
	}
}
