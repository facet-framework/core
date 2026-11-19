// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backingProject builds a project whose single record carries the given
// scenario body, and copies in the real schema so schema validation is
// exercised against the shipped file rather than a fixture of it.
func backingProject(t *testing.T, scenario string) Registry {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		`schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
`+scenario+`lineage:
  owner:
    team: billing
  created_at: "2026-07-30"
`)
	mustWrite(t, filepath.Join(root, "src/invoices.ts"), `// @facet: billing/invoice/list
// @facet-role: primary
export function list() {}
`)
	copySchemas(t, root)
	return Open(root)
}

// copySchemas puts the repository's real schema directory where a project's
// would be.
func copySchemas(t *testing.T, root string) {
	t.Helper()
	dst := filepath.Join(root, "docs/facets/schema")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	src := SourceTestPath(t, "schema")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func lintWithSchema(t *testing.T, reg Registry) Report {
	t.Helper()
	rep, err := Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func schemaErrorsMentioning(r Report, needle string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityError && f.Check == "schema" && strings.Contains(f.Message, needle) {
			out = append(out, f)
		}
	}
	return out
}

// The rejection this whole field turns on: a gap with no explanation must not
// lint clean, because an unexplained gap is indistinguishable from an oversight
// and the point of declaring it is that it is not one.
func TestUnbackedWithoutReasonOrUnblockedByIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
`)
	rep := lintWithSchema(t, reg)
	if !rep.Failed() {
		t.Fatalf("an unbacked scenario with no reason and no unblocked_by must fail the run, got %+v", rep.Findings)
	}
	if got := schemaErrorsMentioning(rep, "reason"); len(got) == 0 {
		t.Errorf("expected the finding to name the missing 'reason', got %+v", rep.Findings)
	}
	if got := schemaErrorsMentioning(rep, "unblocked_by"); len(got) == 0 {
		t.Errorf("expected the finding to name the missing 'unblocked_by', got %+v", rep.Findings)
	}
}

// Half an explanation is still an unexplained gap.
func TestUnbackedWithReasonButNoUnblockedByIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
        reason: the ledger has no rows for a trial account yet
`)
	rep := lintWithSchema(t, reg)
	if !rep.Failed() {
		t.Fatalf("a reason without an unblocker must still fail, got %+v", rep.Findings)
	}
	if got := schemaErrorsMentioning(rep, "unblocked_by"); len(got) == 0 {
		t.Errorf("expected the finding to name the missing 'unblocked_by', got %+v", rep.Findings)
	}
}

// The same obligation applies to a per-variant override, or the override hides
// the gap it was written to expose.
func TestUnbackedVariantOverrideWithoutExplanationIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: backed
        by_variant:
          native:
            state: unbacked
`)
	rep := lintWithSchema(t, reg)
	if !rep.Failed() {
		t.Fatalf("an unexplained per-variant gap must fail, got %+v", rep.Findings)
	}
	if got := schemaErrorsMentioning(rep, "reason"); len(got) == 0 {
		t.Errorf("expected the finding to name the missing 'reason', got %+v", rep.Findings)
	}
}

func TestFullyExplainedUnbackedScenarioIsAccepted(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
        reason: the ledger has no rows for a trial account yet
        unblocked_by: billing/ledger/entries
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("an explained gap is a valid record, got %+v", errorsOnly(rep))
	}
}

// A record may omit backing entirely and must keep its
// meaning: absent is backed, and nothing is reported.
func TestAbsentBackingIsSilent(t *testing.T) {
	reg := backingProject(t, `    - id: list-shown
      statement: |
        Given invoices exist
        Then they are listed
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a record with no backing block must stay valid, got %+v", errorsOnly(rep))
	}
	for _, f := range rep.Findings {
		if f.Check == "backing" {
			t.Fatalf("absent backing must produce no finding, got %+v", f)
		}
	}
}

// Declaring a gap is the honest act the field exists for. Warning about it
// would teach people to go back to prose, so it must stay silent.
func TestDeclaredGapIsNotItselfReported(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
        reason: the ledger has no rows for a trial account yet
        unblocked_by: a-store-we-have-not-built
`)
	for _, f := range lintWithSchema(t, reg).Findings {
		if f.Check == "backing" {
			t.Fatalf("a declared, explained gap must produce no finding, got %+v", f)
		}
	}
}

// The counterpart of the flag-retirement warning: what was going to supply the
// data has shipped, and the record was not told.
func TestUnblockerThatWentActiveIsWarned(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
        reason: the ledger has no rows yet
        unblocked_by: billing/ledger/entries
`)
	mustWrite(t, filepath.Join(reg.Root, "docs/facets/features/billing/ledger/entries.yaml"),
		`schema_version: "0.1"
id: billing/ledger/entries
lifecycle: active
intent:
  summary: |
    Stores the ledger entries every billing view reads from.
contract:
  scenarios:
    - id: entry-written
      statement: |
        Given a charge
        Then a ledger entry is written
lineage:
  owner:
    team: billing
  created_at: "2026-07-30"
`)
	mustWrite(t, filepath.Join(reg.Root, "src/ledger.ts"), `// @facet: billing/ledger/entries
// @facet-role: primary
export function write() {}
`)

	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a stale unblocker is a question, not a broken build: %+v", errorsOnly(rep))
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Check == "backing" && f.Severity == SeverityWarning && strings.Contains(f.Message, "billing/ledger/entries") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning that the named unblocker is now active, got %+v", rep.Findings)
	}
}

// An unblocker that is not an id cannot be resolved and must not be guessed at:
// a store name or an issue reference is a legitimate answer.
func TestProseUnblockerIsNotResolved(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: unbacked
        reason: the ledger has no rows yet
        unblocked_by: "the billing ledger service, tracked in ACME-1234"
`)
	for _, f := range lintWithSchema(t, reg).Findings {
		if f.Check == "backing" {
			t.Fatalf("a prose unblocker must not be resolved against the registry, got %+v", f)
		}
	}
}

// A per-variant override naming a variant nothing declares is a typo that
// leaves the scenario looking backed everywhere.
func TestBackingOnUndeclaredVariantIsReported(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: backed
        by_variant:
          nativ:
            state: unbacked
            reason: the native store is not shipped
            unblocked_by: sync/store/native
`)
	mustWrite(t, filepath.Join(reg.Root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  web:
    paths: ["src/**"]
  native:
    paths: ["native/**"]
`)
	errs := errorsOnly(lintWithSchema(t, reg))
	var found bool
	for _, f := range errs {
		if f.Check == "backing" && strings.Contains(f.Message, "nativ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error naming the undeclared variant, got %+v", errs)
	}
}

// Without a variant map there is nothing to check a name against, and erroring
// would break every single-variant project that uses an override.
func TestBackingVariantUncheckedWithoutVariantMap(t *testing.T) {
	reg := backingProject(t, `    - id: empty-list-shown
      statement: |
        Given no invoices exist
        Then the empty state is shown
      backing:
        state: backed
        by_variant:
          native:
            state: unbacked
            reason: the native store is not shipped
            unblocked_by: sync/store/native
`)
	for _, f := range errorsOnly(lintWithSchema(t, reg)) {
		if f.Check == "backing" {
			t.Fatalf("no variant map means no variant check, got %+v", f)
		}
	}
}

// Backings is the query surface, so its shape and order are part of the
// contract: sorted, one entry per declaration, silent scenarios omitted.
func TestBackingsFlattensAndSorts(t *testing.T) {
	reg := backingProject(t, `    - id: zebra
      statement: |
        Then something
      backing:
        state: unbacked
        reason: r
        unblocked_by: u
    - id: apple
      statement: |
        Then something else
      backing:
        state: backed
        by_variant:
          web:
            state: unbacked
            reason: r
            unblocked_by: u
    - id: silent
      statement: |
        Then nothing is declared
`)
	records, err := reg.LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	got := records[0].Backings()
	want := []struct{ scenario, variant, state string }{
		{"apple", "", Backed},
		{"apple", "web", Unbacked},
		{"zebra", "", Unbacked},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries (the silent scenario is not one), got %+v", len(want), got)
	}
	for i, w := range want {
		if got[i].Scenario != w.scenario || got[i].Variant != w.variant || got[i].State != w.state {
			t.Errorf("entry %d: got %+v, want %v", i, got[i], w)
		}
	}

	unbacked := UnbackedScenarios(records)
	if len(unbacked) != 2 {
		t.Fatalf("expected two unbacked entries, got %+v", unbacked)
	}
}

// metadata and observability are optional declarations. They add no lint
// rule, so what needs proving is that the shipped schema accepts them and still
// rejects a malformed one — an optional block nothing validates is not a
// contract.
func TestMetadataAndObservabilityAreAccepted(t *testing.T) {
	reg := backingProject(t, `    - id: list-shown
      statement: |
        Then invoices are listed
`)
	path := filepath.Join(reg.Root, "docs/facets/features/billing/invoice/list.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(body)+`metadata:
  discovered_by: discovery-agent
  source_ref: "PR #412"
observability:
  metrics_emitted:
    - name: billing_invoice_list_total
      kind: counter
      labels: [account_tier]
      description: invoice list requests served
  traces_emitted:
    - span_name: billing.invoice.list
      parent_span: http.request
      attributes: [account_id]
  log_events:
    - event: billing.invoice.list.served
      level: info
      attributes: [trace_id]
`)
	if rep := lintWithSchema(t, reg); rep.Failed() {
		t.Fatalf("metadata and observability must validate, got %+v", errorsOnly(rep))
	}
}

func TestObservabilityRejectsUnknownMetricKind(t *testing.T) {
	reg := backingProject(t, `    - id: list-shown
      statement: |
        Then invoices are listed
`)
	path := filepath.Join(reg.Root, "docs/facets/features/billing/invoice/list.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(body)+`observability:
  metrics_emitted:
    - name: billing_invoice_list_total
      kind: timer
`)
	if !lintWithSchema(t, reg).Failed() {
		t.Fatal("'timer' is not an OpenMetrics kind and must be rejected")
	}
}

func TestMetadataRejectsNonStringValue(t *testing.T) {
	reg := backingProject(t, `    - id: list-shown
      statement: |
        Then invoices are listed
`)
	path := filepath.Join(reg.Root, "docs/facets/features/billing/invoice/list.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(body)+`metadata:
  nested:
    not: a string
`)
	if !lintWithSchema(t, reg).Failed() {
		t.Fatal("metadata values are strings — a nested map must be rejected")
	}
}
