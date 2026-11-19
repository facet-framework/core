// Fixtures below anchor features that exist only in a temporary directory,
// so this file opts itself out of anchor scanning: @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// errorCodeRecord writes a minimal feature record whose intent.summary
// carries the given prose — enough for the error-codes check, which reads
// text, not schema validity. lifecycle is deliberately 'planned' rather than
// 'active': these fixtures carry no source anchor, and this file has nothing
// to do with the orphan-records check.
func errorCodeRecord(t *testing.T, root, id, summary string) {
	t.Helper()
	path := filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: planned\nintent:\n  summary: |\n    "+summary+"\n")
}

// The concrete, expensive mistake the register exists to make avoidable: the
// same code assigned to two different teams.
func TestSameCodeAcrossTwoDomainsIsAWarning(t *testing.T) {
	root := project(t, "")
	errorCodeRecord(t, root, "crypto/keys/rotate", "Fails with E1074 when the key store is locked.")
	errorCodeRecord(t, root, "shop/sync/queue", "Retries and eventually surfaces E1074 to the operator.")

	rep := lintProject(t, root)
	found := findings(rep, "error-codes")
	if len(found) != 1 {
		t.Fatalf("want exactly one error-codes finding, got %+v", found)
	}
	f := found[0]
	if f.Severity != SeverityWarning {
		t.Errorf("a code crossing a domain boundary is debt to investigate, not a broken build: %+v", f)
	}
	if f.ID != "E1074" {
		t.Errorf("finding must name the code, got %q", f.ID)
	}
	if !strings.Contains(f.Message, "crypto") || !strings.Contains(f.Message, "shop") {
		t.Errorf("finding must name both domains, got %q", f.Message)
	}
	if !strings.Contains(f.Message, "crypto/keys/rotate") || !strings.Contains(f.Message, "shop/sync/queue") {
		t.Errorf("finding must name both records, got %q", f.Message)
	}
	if rep.Failed() {
		t.Fatal("a warning must not fail the run")
	}
}

// Two records under the same domain sharing a code is not the mistake this
// check exists to catch — a feature split across several records of one
// team's own tree is a normal way to write FACET.
func TestSameCodeWithinOneDomainIsNotAFinding(t *testing.T) {
	root := project(t, "")
	errorCodeRecord(t, root, "crypto/keys/rotate", "Fails with E1074 when the key store is locked.")
	errorCodeRecord(t, root, "crypto/keys/derive", "Also surfaces E1074 for a related failure.")

	found := findings(lintProject(t, root), "error-codes")
	if len(found) != 0 {
		t.Fatalf("want no finding for a code reused within one domain, got %+v", found)
	}
}

// A record's own repeated mention of a code — in its summary and again in a
// scenario statement — is one record talking about one code, not a
// collision.
func TestARecordsOwnRepeatedMentionIsNotAFinding(t *testing.T) {
	root := project(t, "")
	path := filepath.Join(root, "docs", "facets", "features", "crypto", "keys", "rotate.yaml")
	writeFile(t, path, `schema_version: "0.1"
id: crypto/keys/rotate
lifecycle: planned
intent:
  summary: |
    Fails with E1074 when the key store is locked.
contract:
  scenarios:
    - id: locked-store
      statement: |
        Given a locked key store
        When rotation is attempted
        Then it fails with E1074
`)

	found := findings(lintProject(t, root), "error-codes")
	if len(found) != 0 {
		t.Fatalf("want no finding for a record's own repeated mention, got %+v", found)
	}
}

// The check is skippable like every other, via '--skip error-codes'.
func TestErrorCodesCheckCanBeSkipped(t *testing.T) {
	root := project(t, "")
	errorCodeRecord(t, root, "crypto/keys/rotate", "Fails with E1074 when the key store is locked.")
	errorCodeRecord(t, root, "shop/sync/queue", "Retries and eventually surfaces E1074 to the operator.")

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "error-codes": true},
		Today: "2026-09-10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if found := findings(rep, "error-codes"); len(found) != 0 {
		t.Fatalf("--skip error-codes must silence the check, got %+v", found)
	}
}

// A configured error-code pattern determines which identifiers are checked
// for collisions.
func TestErrorCodesCheckUsesTheConfiguredPattern(t *testing.T) {
	root := project(t, "schema_version: \"0.1\"\nerror_codes:\n  pattern: 'ERR-\\d{3}'\n")
	errorCodeRecord(t, root, "crypto/keys/rotate", "Fails with ERR-042 when the key store is locked. E1074 is not this project's pattern.")
	errorCodeRecord(t, root, "shop/sync/queue", "Retries and eventually surfaces ERR-042 to the operator.")

	found := findings(lintProject(t, root), "error-codes")
	if len(found) != 1 {
		t.Fatalf("want one finding for ERR-042 under the configured pattern, got %+v", found)
	}
	if found[0].ID != "ERR-042" {
		t.Errorf("finding must name the configured-pattern code, got %q", found[0].ID)
	}
}
