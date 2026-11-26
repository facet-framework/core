// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// sharedUseRecord writes an active feature record with the given scenarios.
// Each scenario is id, statement and an optional specified_by address.
func sharedUseRecord(t *testing.T, root, records, id string, scenarios ...[3]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\ncontract:\n  scenarios:\n")
	for _, s := range scenarios {
		b.WriteString("    - id: " + s[0] + "\n")
		if s[2] != "" {
			b.WriteString("      specified_by: " + s[2] + "\n")
		}
		b.WriteString("      statement: |\n")
		for _, line := range strings.Split(s[1], "\n") {
			b.WriteString("        " + line + "\n")
		}
	}
	writeFile(t, filepath.Join(root, filepath.FromSlash(records), "features", filepath.FromSlash(id)+".yaml"), b.String())
}

func lintShared(t *testing.T, root string) Report {
	t.Helper()
	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "spec-conformance": true, "lost-anchors": true, "created-at-history": true, "orphan-records": true},
		Today: "2026-11-24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

const subtractSteps = "Given two integers\nWhen one is subtracted from the other\nThen the difference is returned"

func TestIdenticalScenariosInTwoRecordsAreNamedOnce(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/difference", [3]string{"basic", subtractSteps, ""})
	// Different spacing and a conjunction resolve to the same normalised steps.
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/subtraction",
		[3]string{"basic", "Given   two integers\nWhen one is subtracted from the other\nThen  the difference is returned", ""})

	rep := lintShared(t, root)
	got := findings(rep, CheckIdenticalScenarios)
	if len(got) != 1 {
		t.Fatalf("want one identical-scenarios finding, got %+v", got)
	}
	f := got[0]
	if f.Severity != SeverityInfo {
		t.Errorf("severity = %q, want info", f.Severity)
	}
	for _, want := range []string{"arithmetic/calculation/difference#basic", "arithmetic/calculation/subtraction#basic"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not name %s", f.Message, want)
		}
	}
	if rep.Failed() {
		t.Fatalf("info findings must not fail the run: %+v", rep.Findings)
	}
	if rep.SharedClaims == nil || len(rep.SharedClaims.Scenarios) != 1 || len(rep.SharedClaims.Scenarios[0].Members) != 2 {
		t.Fatalf("the JSON back-list must carry the group, got %+v", rep.SharedClaims)
	}
}

// The control case: two genuinely different features share nothing, and
// nothing is reported, not even an empty line per category.
func TestTwoDifferentFeaturesProduceNoSharedClaim(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/addition",
		[3]string{"basic", "Given two integers\nWhen they are added\nThen the sum is returned", ""})
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/subtraction", [3]string{"basic", subtractSteps, ""})
	writeFile(t, filepath.Join(root, "src", "add.go"), "package src\n\n// @facet: arithmetic/calculation/addition\n// @facet-role: primary\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(root, "src", "sub.go"), "package src\n\n// @facet: arithmetic/calculation/subtraction\n// @facet-role: primary\nfunc Subtract(a, b int) int { return a - b }\n")

	rep := lintShared(t, root)
	// The premise: both anchors were read, so silence is not a scan that saw nothing.
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: want 2 anchors scanned, got %d", rep.Checked.Anchors)
	}
	for _, f := range rep.Findings {
		if f.Severity == SeverityInfo {
			t.Errorf("unexpected info finding: %+v", f)
		}
	}
	c := rep.SharedClaims
	if c == nil {
		t.Fatal("the back-list must be present, empty, when nothing is shared")
	}
	if len(c.SpecCases)+len(c.Symbols)+len(c.Primaries)+len(c.Scenarios) != 0 {
		t.Fatalf("want an empty back-list, got %+v", c)
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"shared_claims":{"spec_cases":[],"symbols":[],"primaries":[],"scenarios":[],"incomplete":[]}`) {
		t.Fatalf("empty lists must serialise as [], not null: %s", data)
	}
}

func TestTwoPrimaryAnchorsOnOneSymbolAreMarked(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/difference", [3]string{"basic", "Given a\nWhen b\nThen c", ""})
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/subtraction", [3]string{"basic", "Given x\nWhen y\nThen z", ""})
	writeFile(t, filepath.Join(root, "src", "math.go"), `package src

func Add(a, b int) int { return a + b }

// @facet: arithmetic/calculation/subtraction
// @facet-role: primary
// @facet: arithmetic/calculation/difference
// @facet-role: primary
func Sub(a, b int) int { return a - b }
`)

	rep := lintShared(t, root)
	got := findings(rep, CheckSharedSymbol)
	if len(got) != 1 {
		t.Fatalf("want one shared-symbol finding, got %+v", got)
	}
	if !strings.Contains(got[0].Message, "primary for more than one record") || !strings.Contains(got[0].Message, "Sub at src/math.go:9") {
		t.Errorf("message must mark the double primary and name the symbol: %q", got[0].Message)
	}
	sym := rep.SharedClaims.Symbols[0]
	if !sym.DoublePrimary || sym.Symbol != "Sub" || sym.Line != 9 || len(sym.Anchors) != 2 {
		t.Fatalf("symbol entry = %+v", sym)
	}
}

// A symbol shared by a primary and a related anchor is listed but not marked:
// one feature implemented there, another supported by it, is ordinary code.
func TestASharedSymbolWithOnePrimaryIsNotMarked(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given a\nWhen b\nThen c", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given x\nWhen y\nThen z", ""})
	writeFile(t, filepath.Join(root, "src", "x.go"), "package x\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\nfunc Shared() {}\n")

	rep := lintShared(t, root)
	got := findings(rep, CheckSharedSymbol)
	if len(got) != 1 {
		t.Fatalf("want one shared-symbol finding, got %+v", got)
	}
	if strings.Contains(got[0].Message, "primary for more than one") || rep.SharedClaims.Symbols[0].DoublePrimary {
		t.Fatalf("one primary must not be marked as two: %+v", rep.SharedClaims.Symbols[0])
	}
	if !strings.Contains(got[0].Message, "a/b/two (related)") {
		t.Errorf("an anchor without a role reads as related: %q", got[0].Message)
	}
}

// Two anchors in one file on different symbols are not a shared symbol.
func TestTwoRecordsInOneFileOnDifferentSymbolsAreNotShared(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given a\nWhen b\nThen c", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given x\nWhen y\nThen z", ""})
	writeFile(t, filepath.Join(root, "src", "x.go"), "package x\n\n// @facet: a/b/one\n// @facet-role: primary\nfunc One() {}\n\n// @facet: a/b/two\n// @facet-role: primary\nfunc Two() {}\n")

	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: want 2 anchors, got %d", rep.Checked.Anchors)
	}
	if got := findings(rep, CheckSharedSymbol); len(got) != 0 {
		t.Fatalf("different symbols must not be reported, got %+v", got)
	}
}

func TestOneRecordWithTwoPrimaryImplementationsIsNamed(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "arithmetic/calculation/subtraction", [3]string{"basic", subtractSteps, ""})
	writeFile(t, filepath.Join(root, "src", "diff.go"), "package src\n\n// @facet: arithmetic/calculation/subtraction\n// @facet-role: primary\nfunc Diff(a, b int) int { return a - b }\n")
	writeFile(t, filepath.Join(root, "src", "subtract.go"), "package src\n\n// @facet: arithmetic/calculation/subtraction\n// @facet-role: primary\nfunc Subtract(a, b int) int { return a - b }\n")

	rep := lintShared(t, root)
	got := findings(rep, CheckRepeatedPrimary)
	if len(got) != 1 {
		t.Fatalf("want one repeated-primary finding, got %+v", got)
	}
	for _, want := range []string{"Diff at src/diff.go:5", "Subtract at src/subtract.go:5"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q does not name %s", got[0].Message, want)
		}
	}
	if got[0].Path != "docs/facets/features/arithmetic/calculation/subtraction.yaml" {
		t.Errorf("path = %q, want the record's file", got[0].Path)
	}
}

// Primary anchors in two languages are one implementation per layer, not two
// implementations, and stay silent.
func TestPrimaryAnchorsInTwoLanguagesAreNotRepeated(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/page", [3]string{"s", "Given a\nWhen b\nThen c", ""})
	writeFile(t, filepath.Join(root, "src", "handler.go"), "package src\n\n// @facet: a/b/page\n// @facet-role: primary\nfunc Handle() {}\n")
	writeFile(t, filepath.Join(root, "src", "page.ts"), "// @facet: a/b/page\n// @facet-role: primary\nexport function page() {}\n")

	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: want 2 anchors, got %d", rep.Checked.Anchors)
	}
	if got := findings(rep, CheckRepeatedPrimary); len(got) != 0 {
		t.Fatalf("one primary per language must not be reported, got %+v", got)
	}
}

const sharedRegistries = `
schema_version: "0.1"
registries:
  stock:
    records: apps/stock/docs/facets
    sources: [apps/stock]
  billing:
    records: apps/billing/docs/facets
    sources: [apps/billing]
`

// The back-list of one spec case: users are grouped by the widest boundary
// they cross, and a case used across registries says so.
func TestASharedSpecCaseIsGroupedByScope(t *testing.T) {
	root := project(t, sharedRegistries)
	const purchase = "product:shop/purchase#accepted"
	sharedUseRecord(t, root, "apps/stock/docs/facets", "inventory/stock/reservation",
		[3]string{"reserves", "Given an order\nWhen it is accepted\nThen stock is reserved", purchase})
	sharedUseRecord(t, root, "apps/billing/docs/facets", "invoicing/documents/invoice",
		[3]string{"invoices", "Given an order\nWhen it is accepted\nThen an invoice is issued", purchase})
	// Within one record: the same case named from two scenarios.
	sharedUseRecord(t, root, "apps/stock/docs/facets", "inventory/stock/release",
		[3]string{"first", "Given p\nWhen q\nThen r", "product:shop/cancel#done"},
		[3]string{"second", "Given s\nWhen t\nThen u", "product:shop/cancel#done"})
	// One user only: not shared.
	sharedUseRecord(t, root, "apps/stock/docs/facets", "inventory/stock/count",
		[3]string{"counts", "Given k\nWhen l\nThen m", "product:shop/count#done"})

	rep := lintShared(t, root)
	got := findings(rep, CheckSharedSpecCase)
	if len(got) != 2 {
		t.Fatalf("want two shared-spec-case findings, got %+v", got)
	}
	scopes := map[string]string{}
	for _, c := range rep.SharedClaims.SpecCases {
		scopes[c.Address] = c.Scope
	}
	if scopes[purchase] != "registries" || scopes["product:shop/cancel#done"] != "record" {
		t.Fatalf("scopes = %v", scopes)
	}
	if _, listed := scopes["product:shop/count#done"]; listed {
		t.Fatal("a case with one user is not shared")
	}
	for _, f := range got {
		if strings.Contains(f.Message, purchase) && !strings.Contains(f.Message, "billing:invoicing/documents/invoice#invoices") {
			t.Errorf("the cross-registry user must be named with its registry: %q", f.Message)
		}
	}
	if rep.Failed() {
		t.Fatalf("shared use must never fail the run: %+v", rep.Findings)
	}
}

// Identical scenarios in two registries are two products' own contracts and
// stay silent; within one registry they are named.
func TestIdenticalScenariosAcrossRegistriesAreNotNamed(t *testing.T) {
	root := project(t, sharedRegistries)
	sharedUseRecord(t, root, "apps/stock/docs/facets", "a/b/one", [3]string{"s", subtractSteps, ""})
	sharedUseRecord(t, root, "apps/billing/docs/facets", "a/b/two", [3]string{"s", subtractSteps, ""})

	rep := lintShared(t, root)
	if got := findings(rep, CheckIdenticalScenarios); len(got) != 0 {
		t.Fatalf("want no finding across registries, got %+v", got)
	}
}

// --registry narrows the back-list to entries the registry takes part in, and
// a cross-registry spec case is still found because the other registry's
// records are read.
func TestRegistryFilterKeepsCrossRegistryCases(t *testing.T) {
	root := project(t, sharedRegistries)
	const purchase = "product:shop/purchase#accepted"
	sharedUseRecord(t, root, "apps/stock/docs/facets", "inventory/stock/reservation",
		[3]string{"reserves", "Given an order\nWhen it is accepted\nThen stock is reserved", purchase})
	sharedUseRecord(t, root, "apps/billing/docs/facets", "invoicing/documents/invoice",
		[3]string{"invoices", "Given an order\nWhen it is accepted\nThen an invoice is issued", purchase})
	sharedUseRecord(t, root, "apps/billing/docs/facets", "a/b/one", [3]string{"s", subtractSteps, ""})
	sharedUseRecord(t, root, "apps/billing/docs/facets", "a/b/two", [3]string{"s", subtractSteps, ""})

	rep, err := LintAll(root, LintOptions{
		Skip:     map[string]bool{"schema": true, "knowledge": true, "spec-conformance": true, "lost-anchors": true, "created-at-history": true, "orphan-records": true},
		Registry: "stock",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, CheckSharedSpecCase); len(got) != 1 {
		t.Fatalf("want the cross-registry case, got %+v", got)
	}
	if got := findings(rep, CheckIdenticalScenarios); len(got) != 0 {
		t.Fatalf("billing's own duplicates are not stock's, got %+v", got)
	}
}

// --skip turns off one category, or all of them.
func TestSharedClaimsCanBeSkipped(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", subtractSteps, ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", subtractSteps, ""})
	skip := map[string]bool{"schema": true, "knowledge": true, "lost-anchors": true, "created-at-history": true}

	skip[CheckIdenticalScenarios] = true
	rep, err := LintAll(root, LintOptions{Skip: skip})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, CheckIdenticalScenarios); len(got) != 0 {
		t.Fatalf("skipped category still reported: %+v", got)
	}
	if rep.SharedClaims == nil || len(rep.SharedClaims.Scenarios) != 1 {
		t.Fatal("skipping a category's lines must keep the machine-readable list")
	}

	skip["shared-claims"] = true
	rep, err = LintAll(root, LintOptions{Skip: skip})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SharedClaims != nil {
		t.Fatalf("--skip shared-claims must drop the back-list, got %+v", rep.SharedClaims)
	}
}

// A prose statement that is not Gherkin is compared by its normalised text, so
// a copied scenario is still found; different prose is not.
func TestProseScenariosAreComparedByText(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "The total is shown\nin the header.", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "the total is shown in the  header.", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/three", [3]string{"s", "The total is hidden.", ""})

	rep := lintShared(t, root)
	got := rep.SharedClaims.Scenarios
	if len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("want one pair, got %+v", got)
	}
}

// An unreadable source file does not stop the run; its anchors are not placed
// on a symbol, and the measurement names the file it could not read.
func TestAnUnreadableFileIsSkipped(t *testing.T) {
	claims := ComputeSharedClaims([]SharedClaimsInput{{
		Registry: Registry{Root: t.TempDir()},
		Anchors: []Anchor{
			{ID: "a/b/one", Path: "missing.go", Line: 1, Role: "primary"},
			{ID: "a/b/two", Path: "missing.go", Line: 2, Role: "primary"},
		},
	}})
	if len(claims.Symbols) != 0 || len(claims.Primaries) != 0 {
		t.Fatalf("a file that cannot be read places nothing, got %+v", claims)
	}
	// The lists are then not complete, and say so.
	if len(claims.Incomplete) != 1 || claims.Incomplete[0].Path != "missing.go" || !strings.Contains(claims.Incomplete[0].Reason, "declarations not read") {
		t.Fatalf("want the unread file named as a gap, got %+v", claims.Incomplete)
	}
}

// A run whose only findings are info does not fail; one error beside them does.
func TestInfoFindingsNeverFailARun(t *testing.T) {
	rep := Report{Findings: []Finding{
		{Severity: SeverityInfo, Check: CheckSharedSymbol},
		{Severity: SeverityInfo, Check: CheckIdenticalScenarios},
	}}
	if rep.Failed() {
		t.Fatal("info findings alone must not fail the run")
	}
	rep.Findings = append(rep.Findings, Finding{Severity: SeverityError, Check: "schema"})
	if !rep.Failed() {
		t.Fatal("premise: an error beside them must still fail the run")
	}
}
