// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// recordWithIntent writes a minimal feature record carrying the given intent
// body (already YAML-indented under `intent:`).
func recordWithIntent(t *testing.T, root, registryRecords, id, lifecycle, intent string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(registryRecords), "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: "+lifecycle+"\nintent:\n"+intent)
}

// policyWithIntent writes a minimal policy record — decision_records is
// equally valid under a policy's intent facet (schema/policy.schema.json),
// and the check is judged against the whole project the way lintDependencies
// is, not against features alone.
func policyWithIntent(t *testing.T, root, registryRecords, id, intent string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(registryRecords), "policies", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nintent:\n"+intent)
}

// recordWithObservation writes a minimal feature record carrying the given
// observation body (already YAML-indented under `observation:`).
func recordWithObservation(t *testing.T, root, registryRecords, id, lifecycle, observation string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(registryRecords), "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: "+lifecycle+"\nobservation:\n"+observation)
}

// §11.2 names "Broken link" as a warning category — a linked ADR, runbook, or
// dashboard URL that resolves to nothing. This is the repository-relative
// half of it: a decision_records entry that names no file in the checkout.

func TestDecisionRecordThatExistsIsClean(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "docs", "adr", "0001-thing.md"), "# ADR\n")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - docs/adr/0001-thing.md\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("an existing decision record was reported broken: %+v", got)
	}
}

func TestDecisionRecordThatDoesNotExistWarns(t *testing.T) {
	root := project(t, "")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - docs/adr/never-written.md\n")

	rep := lintProject(t, root)
	got := findings(rep, "broken-links")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", rep.Findings)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("§11.2 lists broken link as a warning, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "docs/adr/never-written.md") {
		t.Errorf("the finding must name the missing path: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "intent.decision_records") {
		t.Errorf("the finding must name the field it came from: %q", got[0].Message)
	}
	if got[0].ID != "ui/panel/data" {
		t.Errorf("want the owning record's id, got %q", got[0].ID)
	}
}

// A fragment addresses a heading inside the file, not a file of its own — the
// existence check is against the path alone.
func TestDecisionRecordFragmentIsStrippedBeforeChecking(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "docs", "plans", "rollout.md"), "# Plan\n")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - docs/plans/rollout.md#step-1\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("a fragment on an existing file was reported broken: %+v", got)
	}
}

// A fragment on a file that is NOT there is still broken — stripping the
// fragment must not accidentally strip the whole reference.
func TestDecisionRecordFragmentOnMissingFileWarns(t *testing.T) {
	root := project(t, "")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - docs/plans/rollout.md#step-1\n")

	rep := lintProject(t, root)
	got := findings(rep, "broken-links")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", rep.Findings)
	}
	if !strings.Contains(got[0].Message, "docs/plans/rollout.md#step-1") {
		t.Errorf("the finding should keep the fragment in the message for a reader to find it, got %q", got[0].Message)
	}
}

// The §11.2 check is narrowed on purpose: an http(s):// value is a URL,
// and resolving it needs the network the check must not have (§6.3
// determinism). It is skipped entirely, not reported.
func TestDecisionRecordURLIsNeverChecked(t *testing.T) {
	root := project(t, "")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - https://example.com/does-not-exist-and-is-never-fetched\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("a URL decision record was resolved against the filesystem: %+v", got)
	}
}

// intent.decision_records exists on policy records too
// (schema/policy.schema.json), and the field's schema is identical to the
// feature one — the check must not assume "features" is the only category
// with an intent facet.
func TestDecisionRecordOnAPolicyRecordIsChecked(t *testing.T) {
	root := project(t, "")
	policyWithIntent(t, root, "docs/facets", "policy/privacy/gdpr-erasure",
		"  summary: erase user data within 30 days\n  decision_records:\n    - docs/adr/never-written.md\n")

	rep := lintProject(t, root)
	got := findings(rep, "broken-links")
	if len(got) != 1 {
		t.Fatalf("want one finding on the policy record, got %+v", rep.Findings)
	}
	if got[0].ID != "policy/privacy/gdpr-erasure" {
		t.Errorf("want the policy's id, got %q", got[0].ID)
	}
}

// observation.alerts[].runbook falls under the same §11.2 category with the
// same path-vs-URL split: a runbook that points at nothing is reported, and
// an alert without any runbook remains a separate signal.

func TestRunbookThatExistsIsClean(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "runbooks", "discount-apply-errors.md"), "# Runbook\n")
	recordWithObservation(t, root, "docs/facets", "checkout/pricing/discount", "active",
		"  alerts:\n    - name: discount-apply-error-spike\n      condition: \"error_rate > 0.05 for 5m\"\n      severity: page\n      runbook: runbooks/discount-apply-errors.md\n")
	writeFile(t, filepath.Join(root, "src", "discount.go"), "// @facet: checkout/pricing/discount\npackage a\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("an existing runbook was reported broken: %+v", got)
	}
}

func TestRunbookThatDoesNotExistWarns(t *testing.T) {
	root := project(t, "")
	recordWithObservation(t, root, "docs/facets", "checkout/pricing/discount", "active",
		"  alerts:\n    - name: discount-apply-error-spike\n      condition: \"error_rate > 0.05 for 5m\"\n      severity: page\n      runbook: runbooks/never-written.md\n")
	writeFile(t, filepath.Join(root, "src", "discount.go"), "// @facet: checkout/pricing/discount\npackage a\n")

	rep := lintProject(t, root)
	got := findings(rep, "broken-links")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", rep.Findings)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("want a warning, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "runbooks/never-written.md") {
		t.Errorf("the finding must name the missing path: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "observation.alerts[0].runbook") {
		t.Errorf("the finding must locate the alert by index: %q", got[0].Message)
	}
}

// A second alert's runbook must be reported by its own index, not the
// first's — otherwise two broken runbooks collapse into one finding a reader
// cannot act on.
func TestSecondAlertsRunbookIsReportedByItsOwnIndex(t *testing.T) {
	root := project(t, "")
	recordWithObservation(t, root, "docs/facets", "checkout/pricing/discount", "active",
		"  alerts:\n"+
			"    - name: first\n      condition: \"a\"\n      severity: page\n      runbook: docs/ok.md\n"+
			"    - name: second\n      condition: \"b\"\n      severity: page\n      runbook: runbooks/never-written.md\n")
	writeFile(t, filepath.Join(root, "docs", "ok.md"), "# OK\n")
	writeFile(t, filepath.Join(root, "src", "discount.go"), "// @facet: checkout/pricing/discount\npackage a\n")

	rep := lintProject(t, root)
	got := findings(rep, "broken-links")
	if len(got) != 1 {
		t.Fatalf("want exactly one finding (the second alert), got %+v", rep.Findings)
	}
	if !strings.Contains(got[0].Message, "observation.alerts[1].runbook") {
		t.Errorf("want the second alert's index, got %q", got[0].Message)
	}
}

func TestRunbookURLIsNeverChecked(t *testing.T) {
	root := project(t, "")
	recordWithObservation(t, root, "docs/facets", "checkout/pricing/discount", "active",
		"  alerts:\n    - name: discount-apply-error-spike\n      condition: \"error_rate > 0.05 for 5m\"\n      severity: page\n      runbook: https://runbooks.example.com/discount-apply-errors\n")
	writeFile(t, filepath.Join(root, "src", "discount.go"), "// @facet: checkout/pricing/discount\npackage a\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("a URL runbook was resolved against the filesystem: %+v", got)
	}
}

// An alert with no runbook at all is a different signal — this check must
// stay silent
// about it rather than reporting an empty path as broken.
func TestAlertWithNoRunbookIsNotABrokenLink(t *testing.T) {
	root := project(t, "")
	recordWithObservation(t, root, "docs/facets", "checkout/pricing/discount", "active",
		"  alerts:\n    - name: discount-apply-error-spike\n      condition: \"error_rate > 0.05 for 5m\"\n      severity: page\n")
	writeFile(t, filepath.Join(root, "src", "discount.go"), "// @facet: checkout/pricing/discount\npackage a\n")

	rep := lintProject(t, root)
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("an alert with no runbook at all was reported: %+v", got)
	}
}

// The check is skippable like every other, via `--skip broken-links` / the
// Skip map lintProject itself uses for schema and knowledge.
func TestBrokenLinksCanBeSkipped(t *testing.T) {
	root := project(t, "")
	recordWithIntent(t, root, "docs/facets", "ui/panel/data", "planned",
		"  summary: does a thing\n  decision_records:\n    - docs/adr/never-written.md\n")

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "broken-links": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "broken-links"); len(got) != 0 {
		t.Fatalf("--skip broken-links did not suppress the check: %+v", got)
	}
}
