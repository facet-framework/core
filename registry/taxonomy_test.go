// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// taxonomyProject builds a project holding one record at the given id, with the
// given scenario count, plus whatever taxonomy body the test supplies. An empty
// taxonomy body writes no file at all, which is the unconfigured case.
func taxonomyProject(t *testing.T, id string, scenarios int, taxonomy string) Registry {
	t.Helper()
	root := t.TempDir()

	var body strings.Builder
	for i := 0; i < scenarios; i++ {
		body.WriteString("    - id: scenario-" + string(rune('a'+i)) + "\n")
		body.WriteString("      statement: |\n")
		body.WriteString("        Given a customer\n        When they act\n        Then something observable happens\n")
	}
	if scenarios == 0 {
		body.WriteString("    []\n")
	}

	segments := strings.Split(id, "/")
	mustWrite(t, filepath.Join(root, "docs/facets/features", segments[0], segments[1], segments[2]+".yaml"),
		`schema_version: "0.1"
id: `+id+`
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
`+body.String()+`lineage:
  owner:
    team: billing
  created_at: "2026-09-11"
`)
	mustWrite(t, filepath.Join(root, "src/invoices.ts"), "// @facet: "+id+"\n// @facet-role: primary\nexport function list() {}\n")

	if taxonomy != "" {
		mustWrite(t, filepath.Join(root, "docs/facets/taxonomy.yaml"), taxonomy)
	}
	return Open(root)
}

const billingTaxonomy = `schema_version: "0.1"
domains:
  billing:
    title: Billing
    purpose: Everything a customer is charged for.
    classification: core
    capabilities:
      invoice:
        title: Invoices
        purpose: What a customer is asked to pay.
`

// The backward-compatibility guarantee, and the reason adoption is a decision
// rather than a migration: a project that declares no taxonomy gets no finding
// from the check, however its ids are shaped.
func TestTaxonomyUncheckedWithoutDeclaration(t *testing.T) {
	reg := taxonomyProject(t, "whatever/nonsense/thing", 3, "")
	if found := findingsFor(lintNoSchema(t, reg), "taxonomy"); len(found) != 0 {
		t.Fatalf("expected no taxonomy findings without a taxonomy, got %+v", found)
	}
}

func TestTaxonomyAcceptsDeclaredID(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 3, billingTaxonomy)
	if found := findingsFor(lintNoSchema(t, reg), "taxonomy"); len(found) != 0 {
		t.Fatalf("expected a declared id to pass, got %+v", found)
	}
}

// An id is immutable, so a domain nobody declared is caught while it is still
// cheap to change or never.
func TestTaxonomyUndeclaredDomainIsError(t *testing.T) {
	reg := taxonomyProject(t, "ledger/invoice/list", 3, billingTaxonomy)
	found := findingsFor(lintNoSchema(t, reg), "taxonomy")
	if len(found) == 0 {
		t.Fatal("expected an error for an undeclared domain")
	}
	if found[0].Severity != SeverityError {
		t.Fatalf("expected an error, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "ledger") {
		t.Fatalf("expected the message to name the domain, got %q", found[0].Message)
	}
	// The hint separates a typo from a missing node, which have opposite
	// remedies; without the siblings the reader cannot tell which they have.
	if !strings.Contains(found[0].Hint, "billing") {
		t.Fatalf("expected the hint to list the declared domains, got %q", found[0].Hint)
	}
}

func TestTaxonomyUndeclaredCapabilityIsError(t *testing.T) {
	reg := taxonomyProject(t, "billing/dunning/list", 3, billingTaxonomy)
	found := findingsFor(lintNoSchema(t, reg), "taxonomy")
	if len(found) == 0 {
		t.Fatal("expected an error for an undeclared capability")
	}
	var got Finding
	for _, f := range found {
		if f.Severity == SeverityError {
			got = f
		}
	}
	if got.Message == "" {
		t.Fatalf("expected an error among %+v", found)
	}
	if !strings.Contains(got.Message, "dunning") {
		t.Fatalf("expected the message to name the capability, got %q", got.Message)
	}
	if !strings.Contains(got.Hint, "invoice") {
		t.Fatalf("expected the hint to list the sibling capabilities, got %q", got.Hint)
	}
	// The whole point of the check is that a feature fitting no capability is
	// usually sized wrong. A hint that sends the reader straight to the file
	// entrenches the error the file exists to prevent.
	if !strings.Contains(got.Hint, "sizing test") {
		t.Fatalf("expected the hint to send the reader to the sizing test first, got %q", got.Hint)
	}
}

// A registry whose decomposition is declared but mostly empty: it is declared and
// most of it stands empty. A warning, because declaring a capability before its
// first record is the intended order.
func TestTaxonomyEmptyCapabilityIsWarning(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 3, billingTaxonomy+`      dunning:
        title: Dunning
`)
	found := findingsFor(lintNoSchema(t, reg), "taxonomy")
	if len(found) != 1 {
		t.Fatalf("expected one finding for the empty capability, got %+v", found)
	}
	if found[0].Severity != SeverityWarning {
		t.Fatalf("expected a warning, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "dunning") {
		t.Fatalf("expected the message to name the capability, got %q", found[0].Message)
	}
}

func TestTaxonomyEmptyDomainIsWarning(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 3, billingTaxonomy+`  catalog:
    title: Catalog
    capabilities:
      search:
        title: Search
`)
	found := findingsFor(lintNoSchema(t, reg), "taxonomy")
	if len(found) != 1 {
		t.Fatalf("expected one finding for the empty domain, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "catalog") {
		t.Fatalf("expected the message to name the domain, got %q", found[0].Message)
	}
	// One finding, not one per capability beneath it: an empty domain is one
	// decision, and reporting its children separately would make a single
	// omission look like several.
	if strings.Contains(found[0].Message, "search") {
		t.Fatalf("expected the domain reported once, not per capability: %q", found[0].Message)
	}
}

func TestTaxonomySkippable(t *testing.T) {
	reg := taxonomyProject(t, "ledger/invoice/list", 3, billingTaxonomy)
	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true, "taxonomy": true}})
	if err != nil {
		t.Fatal(err)
	}
	if found := findingsFor(rep, "taxonomy"); len(found) != 0 {
		t.Fatalf("expected --skip taxonomy to silence the check, got %+v", found)
	}
}

func TestClassificationInheritsFromDomain(t *testing.T) {
	tax, err := LoadTaxonomy(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if tax.Configured() {
		t.Fatal("an absent taxonomy must not report itself as configured")
	}

	root := t.TempDir()
	path := filepath.Join(root, "taxonomy.yaml")
	mustWrite(t, path, billingTaxonomy+`      dunning:
        title: Dunning
        classification: supporting
`)
	tax, err = LoadTaxonomy(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := tax.ClassificationOf("billing", "invoice"); got != ClassificationCore {
		t.Fatalf("expected a capability with no classification to inherit the domain's, got %q", got)
	}
	if got := tax.ClassificationOf("billing", "dunning"); got != ClassificationSupporting {
		t.Fatalf("expected the capability's own classification to win, got %q", got)
	}
	// Absent means undecided. A default of "supporting" would put a judgement
	// nobody made into every report built on it.
	if got := tax.ClassificationOf("nothing", "here"); got != "" {
		t.Fatalf("expected an unknown node to classify as undecided, got %q", got)
	}
}

// --- sizing ---

func TestSizingSingleScenarioIsWarning(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 1, "")
	found := findingsFor(lintNoSchema(t, reg), "sizing")
	if len(found) != 1 {
		t.Fatalf("expected one sizing finding, got %+v", found)
	}
	if found[0].Severity != SeverityWarning {
		t.Fatalf("expected a warning, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Hint, "2.1.1") {
		t.Fatalf("expected the hint to cite the gate, got %q", found[0].Hint)
	}
}

func TestSizingAcceptsTheBand(t *testing.T) {
	for _, n := range []int{2, 5, 9, 12} {
		reg := taxonomyProject(t, "billing/invoice/list", n, "")
		if found := findingsFor(lintNoSchema(t, reg), "sizing"); len(found) != 0 {
			t.Fatalf("expected %d scenarios to pass, got %+v", n, found)
		}
	}
}

func TestSizingCeilingIsWarning(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 13, "")
	found := findingsFor(lintNoSchema(t, reg), "sizing")
	if len(found) != 1 {
		t.Fatalf("expected one sizing finding, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "13") {
		t.Fatalf("expected the message to give the count, got %q", found[0].Message)
	}
}

// Two findings for one cause would read as two defects. lintNoScenarios says
// something stronger about zero, so sizing stays out of it.
func TestSizingLeavesZeroToNoScenarios(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 0, "")
	rep := lintNoSchema(t, reg)
	if found := findingsFor(rep, "sizing"); len(found) != 0 {
		t.Fatalf("expected sizing to leave the zero case alone, got %+v", found)
	}
	if found := findingsFor(rep, "no-scenarios"); len(found) != 1 {
		t.Fatalf("expected no-scenarios to own the zero case, got %+v", found)
	}
}

// A planned stub may omit its contract rather than invent one; taxonomy
// checks accept this lifecycle state without requiring active-record content.
func TestSizingSilentOnPlanned(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		`schema_version: "0.1"
id: billing/invoice/list
lifecycle: planned
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
    - id: only-one
      statement: |
        Given a customer
        When they open the list
        Then their invoices are shown
lineage:
  owner:
    team: billing
  created_at: "2026-09-11"
`)
	if found := findingsFor(lintNoSchema(t, Open(root)), "sizing"); len(found) != 0 {
		t.Fatalf("expected sizing to stay silent on a planned record, got %+v", found)
	}
}

// A mistyped key in the taxonomy is otherwise silent: it unmarshals to an
// empty map, so every id beneath it becomes an undeclared-capability error and
// the file that caused them is the one place nothing points at.
func TestMalformedTaxonomyIsCaughtBySchema(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		`schema_version: "0.1"
id: billing/invoice/list
lifecycle: planned
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios: []
lineage:
  owner:
    team: billing
  created_at: "2026-09-11"
`)
	mustWrite(t, filepath.Join(root, "docs/facets/taxonomy.yaml"), `schema_version: "0.1"
domains:
  billing:
    title: Billing
    capabilitys:
      invoice:
        title: Invoices
`)
	copySchemas(t, root)

	found := findingsFor(lintWithSchema(t, Open(root)), "schema")
	var named bool
	for _, f := range found {
		if strings.Contains(f.Path, "taxonomy.yaml") {
			named = true
		}
	}
	if !named {
		t.Fatalf("expected a schema finding naming taxonomy.yaml, got %+v", found)
	}
}

func TestBrokenTaxonomyIsAFindingNotAnAbort(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 3, "schema_version: \"0.1\"\ndomains: [\n")

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true}})
	if err != nil {
		t.Fatalf("a broken taxonomy must not abort the run: %v", err)
	}
	found := findingsFor(rep, "taxonomy")
	if len(found) != 1 || found[0].Severity != SeverityError {
		t.Fatalf("want one error naming the file, got %+v", found)
	}
	if !strings.Contains(found[0].Path, TaxonomyName) {
		t.Fatalf("want the finding on %s, got %q", TaxonomyName, found[0].Path)
	}
	// The other checks must still have run. sizing is unconditional, so a
	// record with three scenarios produces no finding — but the report has to
	// carry the record count, which a bailed-out run never sets.
	if rep.Checked.Records == 0 {
		t.Fatal("the rest of the suite did not run")
	}
}

// A skipped taxonomy check does not load the taxonomy file, allowing
// other checks to run even when that optional file is malformed.
func TestSkipTaxonomySurvivesABrokenFile(t *testing.T) {
	reg := taxonomyProject(t, "billing/invoice/list", 3, "schema_version: \"0.1\"\ndomains: [\n")

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true, "taxonomy": true}})
	if err != nil {
		t.Fatalf("--skip taxonomy must get past a broken taxonomy: %v", err)
	}
	if found := findingsFor(rep, "taxonomy"); len(found) != 0 {
		t.Fatalf("want the check silenced entirely, got %+v", found)
	}
	if rep.Checked.Records == 0 {
		t.Fatal("the rest of the suite did not run")
	}
}
