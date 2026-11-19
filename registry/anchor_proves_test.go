// Fixtures here contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// scenarioRecord writes a minimal feature record whose contract.scenarios is
// supplied whole, since both checks below are entirely about the
// relationship between a record's declared scenarios and what anchors it.
func scenarioRecord(t *testing.T, root, id, contract string) {
	t.Helper()
	path := filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml")
	body := "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\ncontract:\n  scenarios:\n" + contract
	writeFile(t, path, body)
}

// scenarioRecordNoScenarios writes a schema-valid empty scenarios list.
func scenarioRecordNoScenarios(t *testing.T, root, id string) {
	t.Helper()
	path := filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\ncontract:\n  scenarios: []\n")
}

// --- no-scenarios (P2) ---

// An anchored record with an empty scenario list must warn: code exists
// without a declared promise for its anchors to reference.
func TestNoScenariosWarnsOnAnchoredRecordWithEmptyScenarios(t *testing.T) {
	root := project(t, "")
	scenarioRecordNoScenarios(t, root, "ui/panel/data")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	rep := lintProject(t, root)
	got := findings(rep, "no-scenarios")
	if len(got) != 1 {
		t.Fatalf("want one no-scenarios warning, got %+v", got)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("no-scenarios must warn, not error, got %q", got[0].Severity)
	}
	if got[0].ID != "ui/panel/data" {
		t.Errorf("finding must name the record, got %q", got[0].ID)
	}
	if rep.Failed() {
		t.Fatal("a warning must not fail the run")
	}
}

// A record with at least one declared scenario must never trigger
// no-scenarios, anchored or not.
func TestNoScenariosCleanWhenScenariosDeclared(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "ui/panel/data", `    - id: data-loads
      statement: |
        Then data loads
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	if got := findings(lintProject(t, root), "no-scenarios"); len(got) != 0 {
		t.Fatalf("a record with declared scenarios must not warn, got %+v", got)
	}
}

// A record with empty contract.scenarios but no anchor at all is not this
// check's business — an unanchored record with no scenarios is simply a
// record nobody has fleshed out yet, and orphan-records already has an
// opinion about the "no anchor" half.
func TestNoScenariosSilentWhenRecordHasNoAnchor(t *testing.T) {
	root := project(t, "")
	scenarioRecordNoScenarios(t, root, "ui/panel/data")

	if got := findings(lintProject(t, root), "no-scenarios"); len(got) != 0 {
		t.Fatalf("an unanchored record must not trigger no-scenarios, got %+v", got)
	}
}

// The --skip no-scenarios escape hatch must actually turn the check off.
func TestNoScenariosCheckCanBeSkipped(t *testing.T) {
	root := project(t, "")
	scenarioRecordNoScenarios(t, root, "ui/panel/data")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "no-scenarios": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "no-scenarios"); len(got) != 0 {
		t.Fatalf("--skip no-scenarios must suppress the check entirely, got %+v", got)
	}
}

// --- anchor-proves ---

// An @facet-proves: entry naming no scenario in the anchored record must
// fail lint, just as a dangling verification proves reference does.
func TestAnchorProvesDanglingReferenceIsAnError(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "checkout/pricing/discount-code-redemption", `    - id: discount-cap-applied
      statement: |
        Then the discount is capped
`)
	writeFile(t, filepath.Join(root, "src", "discount.rs"),
		"// @facet: checkout/pricing/discount-code-redemption\n"+
			"// @facet-role: primary\n"+
			"// @facet-proves: does-not-exist-xyz\n")

	got := findings(lintProject(t, root), "anchor-proves")
	if len(got) != 1 {
		t.Fatalf("want one anchor-proves error, got %+v", got)
	}
	if got[0].Severity != SeverityError {
		t.Errorf("a dangling @facet-proves: reference must error, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "does-not-exist-xyz") {
		t.Errorf("finding must name the dangling id: %q", got[0].Message)
	}
	if got[0].ID != "checkout/pricing/discount-code-redemption" {
		t.Errorf("finding must name the record, got %q", got[0].ID)
	}
}

// A valid @facet-proves id, matching a real contract scenario, must lint
// clean.
func TestAnchorProvesValidReferenceIsClean(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "checkout/pricing/discount-code-redemption", `    - id: discount-cap-applied
      statement: |
        Then the discount is capped
`)
	writeFile(t, filepath.Join(root, "src", "discount.rs"),
		"// @facet: checkout/pricing/discount-code-redemption\n"+
			"// @facet-role: primary\n"+
			"// @facet-proves: discount-cap-applied\n")

	if got := findings(lintProject(t, root), "anchor-proves"); len(got) != 0 {
		t.Fatalf("a valid @facet-proves: reference was reported: %+v", got)
	}
}

// Two ids on one @facet-proves line, one real and one dangling, must
// produce exactly one error — for the dangling id alone.
func TestAnchorProvesOneValidOneDanglingIsOneError(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "checkout/pricing/discount-code-redemption", `    - id: discount-cap-applied
      statement: |
        Then the discount is capped
`)
	writeFile(t, filepath.Join(root, "src", "discount.rs"),
		"// @facet: checkout/pricing/discount-code-redemption\n"+
			"// @facet-role: primary\n"+
			"// @facet-proves: discount-cap-applied, expired-code-rejected\n")

	got := findings(lintProject(t, root), "anchor-proves")
	if len(got) != 1 {
		t.Fatalf("want one anchor-proves error, got %+v", got)
	}
	if !strings.Contains(got[0].Message, "expired-code-rejected") {
		t.Errorf("finding must name the dangling id, got %q", got[0].Message)
	}
	if strings.Contains(got[0].Message, "discount-cap-applied") {
		t.Errorf("the valid id must not appear in the error, got %q", got[0].Message)
	}
}

// An anchor without @facet-proves claims no scenario coverage, so the absent
// tag produces no finding.
func TestAnchorProvesAbsentTagIsNotAnError(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "checkout/pricing/discount-code-redemption", `    - id: discount-cap-applied
      statement: |
        Then the discount is capped
`)
	writeFile(t, filepath.Join(root, "src", "discount.rs"),
		"// @facet: checkout/pricing/discount-code-redemption\n// @facet-role: primary\n")

	rep := lintProject(t, root)
	if got := findings(rep, "anchor-proves"); len(got) != 0 {
		t.Fatalf("an anchor with no @facet-proves: tag must not be reported, got %+v", got)
	}
	if rep.Failed() {
		t.Fatal("a record with no @facet-proves: tags must not fail the run")
	}
}

// The --skip anchor-proves escape hatch must actually turn the check off.
func TestAnchorProvesCheckCanBeSkipped(t *testing.T) {
	root := project(t, "")
	scenarioRecord(t, root, "checkout/pricing/discount-code-redemption", `    - id: discount-cap-applied
      statement: |
        Then the discount is capped
`)
	writeFile(t, filepath.Join(root, "src", "discount.rs"),
		"// @facet: checkout/pricing/discount-code-redemption\n"+
			"// @facet-role: primary\n"+
			"// @facet-proves: does-not-exist-xyz\n")

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "anchor-proves": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "anchor-proves"); len(got) != 0 {
		t.Fatalf("--skip anchor-proves must suppress the check entirely, got %+v", got)
	}
}
