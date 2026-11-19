// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// A deviation with nothing but a claim is the state this field must not allow.
// The obligation mirrors the one §3.2.1 puts on an unbacked scenario: a defect
// asserted without saying what in the code makes it one is indistinguishable
// from an author who mistrusted the feature, and a reader has nothing to check.
func TestDeviationWithoutReasonIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Given a delete for another tenant's row
        Then the row is deleted regardless of tenant
      deviation:
        confirmed_by: code-review
`)
	rep := lintWithSchema(t, reg)
	if !rep.Failed() {
		t.Fatalf("a deviation with no reason must fail the run, got %+v", rep.Findings)
	}
	if got := schemaErrorsMentioning(rep, "reason"); len(got) == 0 {
		t.Errorf("expected the finding to name the missing 'reason', got %+v", rep.Findings)
	}
}

// The whole field, said fully: what the code does that nobody wants, how that
// was established, and where the fix is tracked.
func TestFullyStatedDeviationIsAccepted(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Given deleteDataSet is called with another tenant's id
        When the query runs with no tenant predicate applied
        Then the row is found and deleted regardless of tenant
      deviation:
        reason: |
          A $scopeFilter is built and never applied to the query. A sibling
          method in the same controller checks the tenant directly.
        confirmed_by: code-review
        tracked_as: "ACME-1234"
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a fully stated deviation is a valid record, got %+v", errorsOnly(rep))
	}
	for _, f := range rep.Findings {
		if f.Check == "deviation" {
			t.Fatalf("declaring a deviation must not itself be a finding, got %+v", f)
		}
	}
}

// The value set is closed for the reason `defect_fix.recorded_by` closes its
// own: a reader weighs the claim by how it was established, and a free string
// would let every registry invent a fourth kind of evidence that means
// something different in each one.
func TestDeviationRejectsAnUndeclaredConfirmedBy(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
        confirmed_by: hunch
`)
	if !lintWithSchema(t, reg).Failed() {
		t.Fatal("'hunch' is not a declared way of confirming a deviation and must be rejected")
	}
}

// The load-bearing omission. There is deliberately no field for the behaviour
// somebody thinks the code ought to have, so that no reader can lift a
// deviation out of the record and treat it as a contract. Any such field must
// be rejected by the schema rather than merely discouraged in prose, because a
// record that validates is a record some tool will read.
func TestDeviationRejectsAFieldForTheIntendedBehaviour(t *testing.T) {
	for _, field := range []string{"expected", "should", "intended_statement"} {
		t.Run(field, func(t *testing.T) {
			reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
        `+field+`: the delete should be scoped to the caller's tenant
`)
			if !lintWithSchema(t, reg).Failed() {
				t.Fatalf("a %q field would let a reader mistake a defect for a contract and must be rejected", field)
			}
		})
	}
}

// The one combination that cannot both be true. `backing: unbacked` says the
// empty state is what ships and, for now, that is intended (§3.2.1); a
// deviation says the behaviour is not intended at all. A scenario asserting
// both leaves a reader unable to answer the only question either field exists
// to answer.
func TestDeviationBesideAnUnbackedScenarioIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
      backing:
        state: unbacked
        reason: the tenant table has no rows yet
        unblocked_by: billing/ledger/entries
`)
	if !lintWithSchema(t, reg).Failed() {
		t.Fatal("a scenario cannot declare both that its gap is intended and that its behaviour is not")
	}
}

// The same contradiction narrowed to one variant is the same contradiction:
// backing_override carries the identical claim for that variant alone, and the
// schema already holds it to the identical obligation.
func TestDeviationBesideAnUnbackedVariantOverrideIsRejected(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
      backing:
        state: backed
        by_variant:
          native:
            state: unbacked
            reason: the native store is not shipped
            unblocked_by: sync/store/native
`)
	if !lintWithSchema(t, reg).Failed() {
		t.Fatal("an unbacked per-variant override beside a deviation is the same contradiction, scoped to a variant")
	}
}

// `backed` and a deviation are not a contradiction and must not be rejected: a
// defect usually has every row it needs and applies them wrongly. The schema
// rejects what cannot be true, never what is merely superfluous.
func TestDeviationBesideABackedScenarioIsAccepted(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
      backing:
        state: backed
`)
	if rep := lintWithSchema(t, reg); rep.Failed() {
		t.Fatalf("a deviation on a backed scenario is a coherent record, got %+v", errorsOnly(rep))
	}
}

// Every record ever written omits this field, and its meaning depends on that
// omission staying legal and staying silent. An absent deviation says nobody
// has declared the behaviour to be a defect — not that anybody vouched for it.
func TestAbsentDeviationIsSilent(t *testing.T) {
	reg := backingProject(t, `    - id: list-shown
      statement: |
        Given invoices exist
        Then they are listed
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a record with no deviation block must stay valid, got %+v", errorsOnly(rep))
	}
	for _, f := range rep.Findings {
		if f.Check == "deviation" {
			t.Fatalf("absent deviation must produce no finding, got %+v", f)
		}
	}
}

// The field's own way of going stale, and the only one a machine can see: the
// record that was going to replace the behaviour has shipped, and the defect is
// still documented as live. The sibling of lintBacking's unblocker warning, and
// a warning for the same reason — a fix landing before the record is updated is
// a legitimate few minutes, not a broken build.
func TestTrackerThatWentActiveIsWarned(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
        confirmed_by: code-review
        tracked_as: billing/invoice/tenant-scoped-delete
`)
	mustWrite(t, filepath.Join(reg.Root, "docs/facets/features/billing/invoice/tenant-scoped-delete.yaml"),
		`schema_version: "0.1"
id: billing/invoice/tenant-scoped-delete
lifecycle: active
intent:
  summary: |
    Scopes every invoice delete to the calling tenant.
contract:
  scenarios:
    - id: delete-is-scoped
      statement: |
        Given a delete for another tenant's row
        Then it is refused
lineage:
  owner:
    team: billing
  created_at: "2026-07-30"
`)
	mustWrite(t, filepath.Join(reg.Root, "src/tenant.ts"), `// @facet: billing/invoice/tenant-scoped-delete
// @facet-role: primary
export function scopedDelete() {}
`)

	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a stale tracker is a question, not a broken build: %+v", errorsOnly(rep))
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Check == "deviation" && f.Severity == SeverityWarning &&
			strings.Contains(f.Message, "billing/invoice/tenant-scoped-delete") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning that the named tracker is now active, got %+v", rep.Findings)
	}
}

// A ticket reference is the common answer and the registry cannot look one up.
// Faulting it would push authors into the vaguest phrasing that avoids the
// noise, which is the state this field exists to end.
func TestProseTrackerIsNotResolved(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
        tracked_as: "ACME-1234, and the thread in #backend"
`)
	for _, f := range lintWithSchema(t, reg).Findings {
		if f.Check == "deviation" {
			t.Fatalf("a prose tracker must not be resolved against the registry, got %+v", f)
		}
	}
}

// An explicit null is how an author says "nothing tracks this", which is a
// different statement from saying nothing — the same distinction
// lineage.superseded_by already carries, and the reason its shape is reused.
func TestNullTrackerIsAcceptedAndNotResolved(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        reason: the scope filter is built and never applied
        tracked_as: null
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("an explicit 'nothing tracks this' is a valid record, got %+v", errorsOnly(rep))
	}
	for _, f := range rep.Findings {
		if f.Check == "deviation" {
			t.Fatalf("a null tracker resolves to nothing and must produce no finding, got %+v", f)
		}
	}
}

// Deviations is the query surface, so its shape and order are part of the
// contract: sorted by scenario, one entry per declaration, and silent scenarios
// omitted — a list carrying every scenario that is not a defect would bury the
// handful that are, which is the whole reason the field exists.
func TestDeviationsFlattensAndSorts(t *testing.T) {
	reg := backingProject(t, `    - id: zebra-drops-the-filter
      statement: |
        Then the filter is dropped
      deviation:
        reason: built and never applied
        confirmed_by: runtime
    - id: apple-accepts-an-expired-token
      statement: |
        Then an expired token is accepted
      deviation:
        reason: the expiry is parsed and discarded
        tracked_as: "ACME-9"
    - id: silent
      statement: |
        Then nothing is declared
`)
	records, err := reg.LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	got := records[0].Deviations()
	want := []struct{ scenario, confirmedBy, trackedAs string }{
		{"apple-accepts-an-expired-token", "", "ACME-9"},
		{"zebra-drops-the-filter", ConfirmedByRuntime, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries (the silent scenario is not one), got %+v", len(want), got)
	}
	for i, w := range want {
		if got[i].Scenario != w.scenario || got[i].ConfirmedBy != w.confirmedBy || got[i].TrackedAs != w.trackedAs {
			t.Errorf("entry %d: got %+v, want %v", i, got[i], w)
		}
	}
	if got[0].Feature != "billing/invoice/list" {
		t.Errorf("an entry must name the record it came from, got %q", got[0].Feature)
	}
	if got[0].Reason == "" {
		t.Error("an entry must carry the reason — it is the only part a reviewer can check")
	}

	if all := DeclaredDeviations(records); len(all) != 2 {
		t.Fatalf("expected two declared deviations across the registry, got %+v", all)
	}
}

// A deviation with no reason is rejected by the schema, but the query surface
// reads records without validating them — the language server, an editor
// mid-keystroke, and 'facet lint --skip schema' all reach it. It must not count
// a claim nobody explained among the ones somebody did, because the reason is
// the only part of an entry a reviewer can check.
func TestDeviationsOmitsAnUnexplainedClaim(t *testing.T) {
	reg := backingProject(t, `    - id: delete-ignores-tenant-scope
      statement: |
        Then the row is deleted regardless of tenant
      deviation:
        confirmed_by: code-review
    - id: export-includes-archived-rows
      statement: |
        Then archived rows are included
      deviation:
        reason: the archived predicate is dropped by the query builder
`)
	records, err := reg.LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	got := records[0].Deviations()
	if len(got) != 1 {
		t.Fatalf("only the explained claim belongs in the listing, got %+v", got)
	}
	if got[0].Scenario != "export-includes-archived-rows" {
		t.Errorf("the wrong entry survived: %+v", got[0])
	}
}
