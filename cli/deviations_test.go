// Fixtures here contain example @facet: tags; the file exercises a command
// rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// deviationsFixture writes a registry with two documented defects: one stating
// how it was confirmed and where it is tracked, one stating neither.
func deviationsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		`schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
    - id: list-shown
      statement: |
        Given invoices exist
        Then they are listed
    - id: delete-ignores-tenant-scope
      statement: |
        Given a delete for another tenant's row
        Then the row is deleted regardless of tenant
      deviation:
        reason: |
          A scope filter is built and never applied to the query.
        confirmed_by: code-review
        tracked_as: "ACME-1234"
    - id: export-includes-archived-rows
      statement: |
        Given an export of the current quarter
        Then archived rows are included
      deviation:
        reason: the archived predicate is dropped by the query builder
lineage:
  owner:
    team: billing
  created_at: "2026-09-16"
`)
	write(t, filepath.Join(root, "src/invoices.ts"),
		"// @facet: billing/invoice/list\n// @facet-role: primary\nexport function list() {}\n")
	return root
}

func runDeviations(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"deviations"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("listing deviations must not fail a run: %v", err)
	}
	return out.String()
}

// The listing is the reason the field is worth filling in: before it, the same
// observation was either written as a contract or left out entirely.
func TestDeviationsListsWhatIsDeclaredAndNothingElse(t *testing.T) {
	out := runDeviations(t, "--root", deviationsFixture(t))

	for _, want := range []string{
		"delete-ignores-tenant-scope",
		"export-includes-archived-rows",
		"A scope filter is built and never applied",
		"code-review",
		"ACME-1234",
		"2 declared deviation(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the listing to carry %q, got:\n%s", want, out)
		}
	}
	// A scenario that declares nothing is a promise, not a defect. Listing it
	// would bury the two that are, which is the state the field exists to end.
	if strings.Contains(out, "list-shown") {
		t.Errorf("a scenario with no deviation must not be listed, got:\n%s", out)
	}
}

// Absent and measured are said differently. The second fixture entry records
// neither how it was confirmed nor where it is tracked, and the listing must
// say so rather than print an empty field that reads as a measured blank.
func TestDeviationsPrintsUnrecordedFieldsAsUnrecorded(t *testing.T) {
	out := runDeviations(t, "--root", deviationsFixture(t))

	var seen bool
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "confirmed:") && !strings.Contains(line, "tracked as:") {
			continue
		}
		if strings.Contains(line, "—") {
			seen = true
			continue
		}
		if strings.TrimSpace(strings.SplitN(line, ":", 2)[1]) == "" {
			t.Errorf("an unrecorded value must read as unrecorded, not as a blank: %q", line)
		}
	}
	if !seen {
		t.Errorf("expected the entry recording neither field to print a dash for both, got:\n%s", out)
	}
}

// Nothing declared is a legitimate and common state, and it must read as
// "nobody has declared one" rather than as an empty report.
func TestDeviationsSaysSoWhenNoneAreDeclared(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		`schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
    - id: list-shown
      statement: |
        Given invoices exist
        Then they are listed
lineage:
  owner:
    team: billing
  created_at: "2026-09-16"
`)
	if out := runDeviations(t, "--root", root); !strings.Contains(out, "No scenario declares itself a deviation") {
		t.Errorf("an empty listing must say what it found nothing of, got:\n%s", out)
	}
}
