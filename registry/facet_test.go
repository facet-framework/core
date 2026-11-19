// This file builds fixtures that contain example @facet: tags. It demonstrates
// the syntax rather than implementing any feature, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newProject builds a minimal FACET project on disk and returns its registry.
func newProject(t *testing.T) Registry {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/payments/checkout/session.yaml"), `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: created
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
knowledge:
  success_signal:
    question: "How does the user know it worked?"
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	mustWrite(t, filepath.Join(root, "src/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	return Open(root)
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintNoSchema(t *testing.T, reg Registry) Report {
	t.Helper()
	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true}})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func errorsOnly(r Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

func TestCleanProjectHasNoErrors(t *testing.T) {
	reg := newProject(t)
	if errs := errorsOnly(lintNoSchema(t, reg)); len(errs) != 0 {
		t.Fatalf("expected no errors, got %+v", errs)
	}
}

// The scenario ids are what a reminder can carry in place of the whole record,
// so they have to survive the round trip through the generic field map.
func TestContractScenariosNamesEachScenario(t *testing.T) {
	reg := newProject(t)
	rec, err := LoadRecord(reg.PathForID("payments/checkout/session"), "features")
	if err != nil {
		t.Fatal(err)
	}
	got := rec.ContractScenarios()
	if len(got) != 1 || got[0] != "created" {
		t.Errorf("ContractScenarios() = %v, want [created]", got)
	}
}

// A record without a contract is a record under construction, not an error.
func TestContractScenariosIsEmptyWithoutAContract(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "r.yaml")
	mustWrite(t, path, "schema_version: \"0.1\"\nid: a/b/c\nlifecycle: planned\n")
	rec, err := LoadRecord(path, "features")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.ContractScenarios(); len(got) != 0 {
		t.Errorf("want nothing, got %v", got)
	}
}

func TestKnowledgeGapIsWarningNotError(t *testing.T) {
	reg := newProject(t)
	rep := lintNoSchema(t, reg)
	if rep.Failed() {
		t.Fatal("an unanswered Knowledge slot must not fail the run")
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Check == "knowledge" && f.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a knowledge warning for the unanswered slot")
	}
}

func TestDanglingAnchorIsReported(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, filepath.Join(reg.Root, "src/refund.ts"), `// @facet: payments/checkout/refund
export function refund() {}
`)
	errs := errorsOnly(lintNoSchema(t, reg))
	if len(errs) != 1 || errs[0].Check != "orphan-ids" {
		t.Fatalf("expected one orphan-ids error, got %+v", errs)
	}
}

func TestActiveRecordWithoutAnchorIsReported(t *testing.T) {
	reg := newProject(t)
	if err := os.Remove(filepath.Join(reg.Root, "src/checkout.ts")); err != nil {
		t.Fatal(err)
	}
	errs := errorsOnly(lintNoSchema(t, reg))
	if len(errs) != 1 || errs[0].Check != "orphan-records" {
		t.Fatalf("expected one orphan-records error, got %+v", errs)
	}
}

// A prefix collision must not mask an orphan: an anchor for
// payments/checkout/session-expiry does not anchor payments/checkout/session.
func TestPrefixCollisionDoesNotMaskOrphan(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, filepath.Join(reg.Root, "src/checkout.ts"), `// @facet: payments/checkout/session-expiry
// @facet-role: primary
export function expire() {}
`)
	var sawOrphanRecord bool
	for _, f := range errorsOnly(lintNoSchema(t, reg)) {
		if f.Check == "orphan-records" && f.ID == "payments/checkout/session" {
			sawOrphanRecord = true
		}
	}
	if !sawOrphanRecord {
		t.Fatal("a longer id sharing a prefix must not count as an anchor")
	}
}

// Documentation showing @facet: as an example does not implement anything.
func TestProseIsNotScanned(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, filepath.Join(reg.Root, "README.md"), "Tag your code:\n\n    @facet: some/made/up-id\n")
	if errs := errorsOnly(lintNoSchema(t, reg)); len(errs) != 0 {
		t.Fatalf("markdown must not contribute anchors, got %+v", errs)
	}
}

// .facetignore keeps a scan away from files that can never carry an anchor.
func TestFacetIgnoreExcludesMatchingFiles(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, filepath.Join(reg.Root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("without an ignore file both anchors are found, got %d", len(anchors))
	}

	mustWrite(t, filepath.Join(reg.Root, IgnoreFile), "vendor/\n")
	anchors, err = ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("the ignored file must not contribute an anchor, got %d", len(anchors))
	}
	if anchors[0].Path == filepath.Join(reg.Root, "vendor/bundle.js") {
		t.Fatal("the surviving anchor is the ignored one")
	}
}

// An ignore file cannot hide a record: records are loaded by their own walk.
func TestFacetIgnoreCannotHideRecords(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, filepath.Join(reg.Root, IgnoreFile), "*.yaml\n")

	records, err := Open(reg.Root).LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records must survive an ignore file that names their extension, got %d", len(records))
	}
}

func TestIDMustMatchPath(t *testing.T) {
	reg := newProject(t)
	path := filepath.Join(reg.Root, "docs/facets/features/payments/checkout/session.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(body[:0])+`schema_version: "0.1"
id: payments/checkout/wrong-name
lifecycle: planned
intent:
  summary: |
    A record whose declared id contradicts where it lives on disk.
contract:
  scenarios: []
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	var found bool
	for _, f := range errorsOnly(lintNoSchema(t, reg)) {
		if f.Check == "id-path" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an id-path error")
	}
}

func TestAnchorRoleIsCaptured(t *testing.T) {
	reg := newProject(t)
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected one anchor, got %d", len(anchors))
	}
	if anchors[0].Role != "primary" {
		t.Fatalf("expected role primary, got %q", anchors[0].Role)
	}
}

func TestNotAdoptedWhenNoRecordsDirectory(t *testing.T) {
	if Open(t.TempDir()).Adopted() {
		t.Fatal("a project without a records directory must not read as adopted")
	}
}

// Provenance: content digests, never timestamps.

func recordArtifact(t *testing.T, reg Registry) Manifest {
	t.Helper()
	mustWrite(t, filepath.Join(reg.Root, "docs/help/pay.md"), "# Paying\nPick a plan.\n")
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	art, err := BuildArtifact(reg, "payments/checkout/session", "docs/help/pay.md",
		Generator{Pipeline: "test"}, nil, anchors)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{SchemaVersion: "0.1"}
	m.Upsert(art)
	return m
}

func checkStates(t *testing.T, reg Registry, m Manifest) []Status {
	t.Helper()
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return CheckManifest(reg, m, anchors)
}

func TestUnchangedArtifactIsCurrent(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	if got := checkStates(t, reg, m)[0].State; got != StateCurrent {
		t.Fatalf("expected current, got %s", got)
	}
}

// The property that defeats mtime-based staleness: a fresh checkout rewrites
// every timestamp but no content, and must not invalidate anything.
func TestTouchingFilesDoesNotInvalidate(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	future := os.Chtimes
	for _, p := range []string{"src/checkout.ts", "docs/help/pay.md"} {
		full := filepath.Join(reg.Root, p)
		st, err := os.Stat(full)
		if err != nil {
			t.Fatal(err)
		}
		if err := future(full, st.ModTime().Add(1e12), st.ModTime().Add(1e12)); err != nil {
			t.Fatal(err)
		}
	}
	if got := checkStates(t, reg, m)[0].State; got != StateCurrent {
		t.Fatalf("changed timestamps must not invalidate, got %s", got)
	}
}

func TestChangedAnchorMakesArtifactStale(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	mustWrite(t, filepath.Join(reg.Root, "src/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() { return "now with a different behaviour" }
`)
	if got := checkStates(t, reg, m)[0].State; got != StateStale {
		t.Fatalf("expected stale, got %s", got)
	}
}

// The untracked-input case: every recorded digest still matches, but the
// feature now resolves to an anchor that did not exist at generation time.
func TestAddedAnchorMakesArtifactStale(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	mustWrite(t, filepath.Join(reg.Root, "src/webhook.ts"), `// @facet: payments/checkout/session
// @facet-role: related
export function hook() {}
`)
	st := checkStates(t, reg, m)[0]
	if st.State != StateStale {
		t.Fatalf("a new anchor must invalidate the artifact, got %s", st.State)
	}
}

func TestDeletedRecordMakesArtifactOrphaned(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	if err := os.Remove(reg.PathForID("payments/checkout/session")); err != nil {
		t.Fatal(err)
	}
	if got := checkStates(t, reg, m)[0].State; got != StateOrphaned {
		t.Fatalf("expected orphaned, got %s", got)
	}
}

func TestManifestRoundTrips(t *testing.T) {
	reg := newProject(t)
	m := recordArtifact(t, reg)
	if err := m.Save(reg.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(reg.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Artifacts) != 1 || loaded.Artifacts[0].Subject.Path != "docs/help/pay.md" {
		t.Fatalf("manifest did not round-trip: %+v", loaded)
	}
}

func TestMissingManifestIsNotAnError(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "generated.lock.yaml"))
	if err != nil {
		t.Fatalf("a project that has generated nothing must not error: %v", err)
	}
	if len(m.Artifacts) != 0 {
		t.Fatal("expected an empty manifest")
	}
}

// An enum violation is a distinct validation error kind; it must render
// as a finding without panicking, just like missing or extra properties.
func TestEnumViolationIsReportedNotPanicked(t *testing.T) {
	reg := newProject(t)
	for _, name := range []string{"facet.schema.json"} {
		src, err := os.ReadFile(SourceTestPath(t, "schema", name))
		if err != nil {
			t.Fatalf("shipped schema not available: %v", err)
		}
		mustWrite(t, filepath.Join(reg.RecordsDir, "schema", name), string(src))
	}
	reg = Open(reg.Root)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "9.9"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    A record declaring a schema version the enum does not permit.
contract:
  scenarios: []
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	rep, err := Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawSchemaError bool
	for _, f := range rep.Findings {
		if f.Check == "schema" && f.Severity == SeverityError {
			sawSchemaError = true
			if f.Message == "" {
				t.Fatal("schema finding must carry a rendered message")
			}
		}
	}
	if !sawSchemaError {
		t.Fatal("expected a schema error for the out-of-enum schema_version")
	}
}

func withRealSchema(t *testing.T, reg Registry) Registry {
	t.Helper()
	src, err := os.ReadFile(SourceTestPath(t, "schema", "facet.schema.json"))
	if err != nil {
		t.Fatalf("shipped schema not available: %v", err)
	}
	mustWrite(t, filepath.Join(reg.RecordsDir, "schema", "facet.schema.json"), string(src))
	return Open(reg.Root)
}

// schemaErrors returns the schema findings a lint run produced, so a test can
// say both "this validated" and "this did not" from the same helper.
func schemaErrors(t *testing.T, reg Registry) []Finding {
	t.Helper()
	rep, err := Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []Finding
	for _, f := range rep.Findings {
		if f.Check == "schema" && f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// A scenario may say which readership its statement was written for, and the
// three states that carry meaning must all survive validation: the two declared
// audiences, and the absence that claims neither.
func TestScenarioAudienceAcceptsBothValuesAndAbsence(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: written-for-product
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
      audience: product
    - id: written-for-engineering
      statement: |
        Given CheckoutSessionStore::insert() is called with a valid row
        When the transaction commits
        Then sessions.status is 'open'
      audience: engineering
    - id: nobody-said
      statement: |
        Given a plan that no longer sells
        When the customer subscribes
        Then the session is refused
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	if found := schemaErrors(t, reg); len(found) != 0 {
		t.Fatalf("a record declaring both audiences and omitting the field once must validate; got %d schema error(s): %v", len(found), found)
	}
}

// The audience enum is closed so validators and consumers use the same
// set of supported audience values.
func TestScenarioAudienceRejectsAnUndeclaredValue(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: created
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
      audience: technical-function
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	found := schemaErrors(t, reg)
	if len(found) == 0 {
		t.Fatal("expected a schema error for an audience value the enum does not declare")
	}
	for _, f := range found {
		if f.Message == "" {
			t.Fatal("schema finding must carry a rendered message")
		}
	}
}

func TestScanFileIsUsableForSingleDocumentChecks(t *testing.T) {
	reg := newProject(t)
	anchors, err := ScanFile(filepath.Join(reg.Root, "src/checkout.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 || anchors[0].ID != "payments/checkout/session" {
		t.Fatalf("unexpected anchors: %+v", anchors)
	}
	if anchors[0].Line != 1 {
		t.Fatalf("expected the anchor on line 1, got %d", anchors[0].Line)
	}
}

func TestIgnoreMarkerCountsOnlyInTheHead(t *testing.T) {
	reg := newProject(t)
	marker := "@facet-ignore" + "-file"

	// hook-lib.sh in miniature: a real anchor up top, and further down a
	// comment explaining the opt-out it does not want.
	var body strings.Builder
	body.WriteString("#!/usr/bin/env sh\n# @facet: payments/checkout/session\n# @facet-role: primary\n")
	for i := 0; i < ignoreHeadLines; i++ {
		body.WriteString("# padding\n")
	}
	body.WriteString("# files that opted out with " + marker + " are skipped\n")

	explains := filepath.Join(reg.Root, "src/lib.sh")
	mustWrite(t, explains, body.String())
	anchors, err := ScanFile(explains)
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("a file that only explains the marker must still be scanned, got %+v", anchors)
	}

	// The same literal inside the window still opts the file out, which is the
	// half that must not regress: an opt-out anywhere within the first
	// ignoreHeadLines lines counts.
	declares := filepath.Join(reg.Root, "src/fixture.sh")
	mustWrite(t, declares, "#!/usr/bin/env sh\n# "+marker+"\n# @facet: payments/checkout/session\n")
	if anchors, err = ScanFile(declares); err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 0 {
		t.Fatalf("a declared opt-out must still skip the file, got %+v", anchors)
	}
}

// The directive is matched against file content, so a file that must contain
// the exact string cannot also be scanned — implementations avoid that by
// splitting the literal. This pins both halves of that behaviour.
func TestIgnoreMarkerMatchesContentNotIntent(t *testing.T) {
	reg := newProject(t)
	marker := "@facet-ignore" + "-file"

	// Source that builds the marker from parts never contains it, so it is
	// scanned normally. This is how the scanner and the language server avoid
	// opting themselves out.
	split := filepath.Join(reg.Root, "src/scanner.go")
	mustWrite(t, split, "package scan\n\n// @facet: payments/checkout/session\n// @facet-role: related\nvar ignore = []byte(\"@facet-ignore\" + \"-file\")\n")
	anchors, err := ScanFile(split)
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("source that splits the literal must still be scanned, got %d anchors", len(anchors))
	}

	// A file whose content does contain the directive is skipped entirely,
	// whatever its anchors claim.
	opted := filepath.Join(reg.Root, "src/fixtures.go")
	mustWrite(t, opted, "// "+marker+"\npackage fixtures\n\n// @facet: some/made/up-id\n")
	anchors, err = ScanFile(opted)
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 0 {
		t.Fatalf("a file carrying the directive must be skipped, got %+v", anchors)
	}
}

// knowledgeRecord renders the project's one record with a single Knowledge slot
// written however a test needs it, so the cases below differ only in the slot.
func knowledgeRecord(slot string) string {
	return `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: created
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
knowledge:
` + slot + `lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`
}

func knowledgeWarnings(t *testing.T, reg Registry) []Finding {
	t.Helper()
	var out []Finding
	for _, f := range lintNoSchema(t, reg).Findings {
		if f.Check == "knowledge" {
			out = append(out, f)
		}
	}
	return out
}

// Two otherwise identical records distinguish a pending slot from an
// owner-declared not-applicable slot with a reason and attribution.
// Only the pending slot receives a missing-answer warning.
func TestNotApplicableSlotIsNotAGap(t *testing.T) {
	reg := newProject(t)
	path := reg.PathForID("payments/checkout/session")

	mustWrite(t, path, knowledgeRecord(`  common_pitfalls:
    question: "What mistakes do users typically make?"
`))
	if got := knowledgeWarnings(t, reg); len(got) != 1 {
		t.Fatalf("an unanswered slot must still warn, got %d finding(s): %+v", len(got), got)
	}

	mustWrite(t, path, knowledgeRecord(`  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI and either passes or fails.
      confirmed_by: team-payments
`))
	if got := knowledgeWarnings(t, reg); len(got) != 0 {
		t.Fatalf("a declared exception must silence its own slot, got %+v", got)
	}
}

// The exception is per slot and never a record-level opt-out. A record that
// declares one question inapplicable still owes the others, and the warning
// must name exactly those — otherwise declaring one exception would buy silence
// about work nobody did.
func TestNotApplicableSilencesOnlyItsOwnSlot(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(`  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI and either passes or fails.
      confirmed_by: team-payments
  success_signal:
    question: "How does the user know it worked?"
`))
	got := knowledgeWarnings(t, reg)
	if len(got) != 1 {
		t.Fatalf("the remaining slot must still warn, got %d finding(s): %+v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "success_signal") {
		t.Errorf("the warning must name the slot still owed, got %q", got[0].Message)
	}
	if strings.Contains(got[0].Message, "common_pitfalls") {
		t.Errorf("the warning must not name the slot declared inapplicable, got %q", got[0].Message)
	}
}

// Declaring a gap or defect is not itself a finding, as specified in
// FACET §3.2.1 and §3.2.2; the declaration records the exception explicitly.
func TestDeclaringNotApplicableIsNeverItselfAFinding(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(`  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI and either passes or fails.
      confirmed_by: team-payments
`))
	rep, err := Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed() {
		t.Fatal("a declared exception must not fail the run")
	}
	for _, f := range rep.Findings {
		if f.Check == "knowledge" || f.Check == "schema" {
			t.Errorf("declaring an exception must produce no finding, got %s: %s", f.Check, f.Message)
		}
	}
}

// Incomplete exceptions are rejected by the schema, including missing
// reason or attribution. This requirement applies to every schema validator,
// including the language server, independently of lint checks.
func TestNotApplicableRequiresAReasonAndAConfirmer(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	for _, tc := range []struct {
		name  string
		names string
		slot  string
	}{
		{"no reason", "reason", `  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable:
      confirmed_by: team-payments
`},
		{"no confirmer", "confirmed_by", `  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI.
`},
		{"empty", "reason", `  common_pitfalls:
    question: "What mistakes do users typically make?"
    not_applicable: {}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(tc.slot))
			// Named rather than counted: 'len(found) > 0' would pass on any
			// schema error at all, including one this fixture introduced by
			// accident somewhere else in the record, and the test would then
			// keep passing after the rule it exists for was removed.
			found := schemaErrors(t, reg)
			if len(found) == 0 {
				t.Fatal("a half-declared exception must not validate")
			}
			if !anyMentions(found, tc.names) {
				t.Fatalf("the finding must name the missing field %q, got %+v", tc.names, found)
			}
		})
	}
}

// anyMentions reports whether some finding's message contains the substring.
func anyMentions(found []Finding, needle string) bool {
	for _, f := range found {
		if strings.Contains(f.Message, needle) {
			return true
		}
	}
	return false
}

// An answer to a question declared to have none is a contradiction a reader
// cannot resolve, and a 'derive_from' beside it instructs a generator to render
// a slot that has just said it has nothing to render. Both are visible without
// reading anything outside the slot, so the schema refuses them — the same
// ground on which a deviation beside an unbacked backing is refused.
func TestNotApplicableCannotCoexistWithAnAnswer(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	for _, tc := range []struct {
		name string
		slot string
	}{
		{"with an answer", `  common_pitfalls:
    question: "What mistakes do users typically make?"
    answer: |
      Pasting a code with surrounding whitespace.
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI.
      confirmed_by: team-payments
`},
		{"with derive_from", `  common_pitfalls:
    question: "What mistakes do users typically make?"
    derive_from: [anchors]
    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI.
      confirmed_by: team-payments
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(tc.slot))
			if found := schemaErrors(t, reg); len(found) == 0 {
				t.Fatal("a slot claiming both an answer and no answer must not validate")
			}
		})
	}
}

// Every record written before this field keeps exactly its meaning: a slot that
// omits it is a gap, as it was. The field's whole value rests on the omission
// staying legal and staying loud.
func TestOmittingNotApplicableStillWarns(t *testing.T) {
	reg := withRealSchema(t, newProject(t))
	if got := knowledgeWarnings(t, reg); len(got) != 1 {
		t.Fatalf("an untouched record must warn exactly as before, got %d finding(s): %+v", len(got), got)
	}
}

// KnowledgeGaps validates exception completeness independently of the schema.
// Only a complete not-applicable declaration suppresses the gap warning,
// including when schema validation is skipped or unavailable.
func TestHalfDeclaredExceptionStillWarnsWithoutTheSchema(t *testing.T) {
	reg := newProject(t)
	for _, tc := range []struct {
		name string
		slot string
	}{
		{"reason only", `    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI.
`},
		{"confirmer only", `    not_applicable:
      confirmed_by: team-payments
`},
		{"both blank", `    not_applicable:
      reason: "   "
      confirmed_by: ""
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(
				"  common_pitfalls:\n    question: \"What mistakes do users typically make?\"\n"+tc.slot))
			got := knowledgeWarnings(t, reg)
			if len(got) != 1 {
				t.Fatalf("a half-declared exception must not buy silence, got %d finding(s): %+v", len(got), got)
			}
			if !strings.Contains(got[0].Message, "common_pitfalls") {
				t.Errorf("the slot must still be named as a gap, got %q", got[0].Message)
			}
		})
	}
}

// Knowledge-gap hints are delivered to agents by the language server and
// registry_validate. They must not suggest owner-only exception declarations,
// which drafting agents are not authorised to make.
func TestTheKnowledgeHintDoesNotNameTheException(t *testing.T) {
	reg := newProject(t)
	got := knowledgeWarnings(t, reg)
	if len(got) != 1 {
		t.Fatalf("expected the standing warning, got %d finding(s): %+v", len(got), got)
	}
	if strings.Contains(got[0].Hint, "not_applicable") {
		t.Errorf("the hint must not offer the exception to whoever is drafting; got %q", got[0].Hint)
	}
	if !strings.Contains(got[0].Hint, "owner") {
		t.Errorf("the hint must point at the owner, got %q", got[0].Hint)
	}
}

// A slot cannot both answer and declare it has no answer. The schema refuses
// it, and so does this: '--skip schema', and a project that never installed
// one, reach the gap check with nothing having judged the record — and there
// the contradiction would otherwise resolve silently in the answer's favour.
func TestAnsweringAndDeclaringNoAnswerIsAnError(t *testing.T) {
	reg := newProject(t)
	for _, tc := range []struct {
		name string
		slot string
	}{
		{"answer", `    answer: |
      Pasting a code with surrounding whitespace.
`},
		{"derive_from", `    derive_from: [anchors]
`},
		{"by_variant answer", `    by_variant:
      web:
        answer: |
          Pasting a code with surrounding whitespace.
`},
		{"by_variant derive_from", `    by_variant:
      web:
        derive_from: [anchors]
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(
				"  common_pitfalls:\n    question: \"What mistakes do users typically make?\"\n"+tc.slot+
					`    not_applicable:
      reason: |
        Nobody interacts with this; it runs in CI.
      confirmed_by: team-payments
`))
			rep := lintNoSchema(t, reg)
			var found bool
			for _, f := range rep.Findings {
				if f.Check == "knowledge" && f.Severity == SeverityError {
					found = true
					if !strings.Contains(f.Message, "common_pitfalls") {
						t.Errorf("the error must name the slot, got %q", f.Message)
					}
				}
			}
			if !found {
				t.Fatalf("the contradiction must be an error on the Go path too, got %+v", rep.Findings)
			}
		})
	}
}

// The negative: per-variant answers on a slot that declares no exception are
// ordinary and must stay legal. The exclusion is a property of the declaration,
// not of by_variant.
func TestPerVariantAnswersStayLegalWithoutAnException(t *testing.T) {
	reg := newProject(t)
	mustWrite(t, reg.PathForID("payments/checkout/session"), knowledgeRecord(`  common_pitfalls:
    question: "What mistakes do users typically make?"
    by_variant:
      web:
        answer: |
          Pasting a code with surrounding whitespace.
`))
	for _, f := range lintNoSchema(t, reg).Findings {
		if f.Check == "knowledge" && f.Severity == SeverityError {
			t.Fatalf("a per-variant answer without an exception must not be an error: %s", f.Message)
		}
	}
}
