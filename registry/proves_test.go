// Fixtures here contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// provesRecord writes a minimal feature record whose contract.scenarios and
// verification blocks are supplied whole, since the proves check is entirely
// about the relationship between the two.
func provesRecord(t *testing.T, root, id, contract, verification string) {
	t.Helper()
	path := filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml")
	body := "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\ncontract:\n  scenarios:\n" + contract
	if verification != "" {
		body += "verification:\n" + verification
	}
	writeFile(t, path, body)
}

// A proves entry naming no contract scenario is a reference error.
func TestDanglingProvesReferenceIsAnError(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Given a panel
        Then data loads
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [does-not-exist-xyz]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	got := findings(lintProject(t, root), "proves")
	var errs []Finding
	for _, f := range got {
		if f.Severity == SeverityError {
			errs = append(errs, f)
		}
	}
	if len(errs) != 1 {
		t.Fatalf("want one proves error, got %+v", got)
	}
	if !strings.Contains(errs[0].Message, "does-not-exist-xyz") {
		t.Errorf("finding must name the dangling id: %q", errs[0].Message)
	}
	if errs[0].ID != "ui/panel/data" {
		t.Errorf("finding must name the record, got %q", errs[0].ID)
	}
}

// Four dangling references in one record must be reported once each, not
// collapsed into one finding.
func TestFourDanglingProvesReferencesAreFourErrors(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: "a.spec.ts: one"
      proves: [does-not-exist-xyz]
    - id: "a.spec.ts: two"
      proves: [does-not-exist-xyz]
  integration_tests:
    - id: "b.spec.ts: three"
      proves: [does-not-exist-xyz]
  e2e_tests:
    - id: "c.spec.ts: four"
      proves: [does-not-exist-xyz]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	got := findings(lintProject(t, root), "proves")
	var errs []Finding
	for _, f := range got {
		if f.Severity == SeverityError {
			errs = append(errs, f)
		}
	}
	if len(errs) != 4 {
		t.Fatalf("want four proves errors, got %d: %+v", len(errs), got)
	}
}

// A `proves` id that matches a real contract scenario must lint clean.
func TestValidProvesReferenceIsClean(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	got := findings(lintProject(t, root), "proves")
	for _, f := range got {
		if f.Severity == SeverityError {
			t.Fatalf("a valid proves reference was reported: %+v", f)
		}
	}
}

// A missing verification block is not an error: it asserts no proof.
// Missing coverage is reported as a warning.
func TestNoVerificationBlockIsNotAnError(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`, "")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	got := findings(lintProject(t, root), "proves")
	for _, f := range got {
		if f.Severity == SeverityError {
			t.Fatalf("a record with no verification block must not error, got %+v", f)
		}
	}
}

// A scenario with no proof anywhere is the sibling warning: real coverage
// numbers depend on this being visible rather than silently counted as
// covered.
func TestScenarioWithNoProofWarns(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
    - id: data-errors
      statement: |
        Then an error is shown
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	writeFile(t, filepath.Join(root, "src", "data.spec.ts"), "it('loads', () => {})\n")

	rep := lintProject(t, root)
	got := findings(rep, "proves")
	var warns []Finding
	for _, f := range got {
		if f.Severity == SeverityWarning {
			warns = append(warns, f)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("want one proves warning, got %+v", got)
	}
	if !strings.Contains(warns[0].Message, "data-errors") {
		t.Errorf("warning must name the unproven scenario, got %q", warns[0].Message)
	}
	if strings.Contains(warns[0].Message, "data-loads") {
		t.Errorf("the proven scenario must not appear in the warning, got %q", warns[0].Message)
	}
	if rep.Failed() {
		t.Fatal("an unproven scenario is a warning, not a broken build")
	}
}

// A dangling reference does not count as proof of the scenario it mistypes —
// otherwise the error and the warning would disagree with each other on the
// exact case the check exists for.
func TestDanglingProvesDoesNotSuppressTheUnprovenWarning(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [data-load]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	rep := lintProject(t, root)
	got := findings(rep, "proves")
	var errs, warns int
	for _, f := range got {
		if f.Severity == SeverityError {
			errs++
		} else {
			warns++
		}
	}
	if errs != 1 {
		t.Fatalf("want one error for the dangling id, got %d: %+v", errs, got)
	}
	if warns != 1 {
		t.Fatalf("want one warning — the real scenario is still unproven, got %d: %+v", warns, got)
	}
}

// A scenario proven by more than one test, across different verification
// buckets, is ordinary and must produce no finding.
func TestScenarioProvenByMultipleTestsIsClean(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [data-loads]
  e2e_tests:
    - id: "e2e/panel.spec.ts: loads end to end"
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	if got := findings(lintProject(t, root), "proves"); len(got) != 0 {
		t.Fatalf("a doubly-proven scenario was reported: %+v", got)
	}
}

// The `--skip proves` escape hatch must actually turn the check off.
func TestProvesCheckCanBeSkipped(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: "src/data.spec.ts: loads"
      proves: [does-not-exist-xyz]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "proves": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "proves"); len(got) != 0 {
		t.Fatalf("--skip proves must suppress the check entirely, got %+v", got)
	}
}
