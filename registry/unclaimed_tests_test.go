// Fixtures here contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// claimRecord writes one anchored record whose verification block is supplied
// whole, since this check is entirely about what that block names.
func claimRecord(t *testing.T, root, id, verification string) {
	t.Helper()
	body := "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\nintent:\n  summary: |\n    Does a thing.\ncontract:\n  scenarios:\n    - id: it-works\n      statement: |\n        Given a\n        When b\n        Then c\n"
	if verification != "" {
		body += "verification:\n" + verification
	}
	writeFile(t, filepath.Join(root, "docs/facets/features", filepath.FromSlash(id)+".yaml"), body)
	writeFile(t, filepath.Join(root, "src", "impl.ts"), "// @facet: "+id+"\n// @facet-role: primary\nexport function run() {}\n")
}

// messagesOf collects what the findings actually said, since this check's
// whole output is the list of names in its message.
func messagesOf(all []Finding) []string {
	out := make([]string, 0, len(all))
	for _, f := range all {
		out = append(out, f.Message)
	}
	sort.Strings(out)
	return out
}

// A test file unclaimed by verification must be named even if it passes;
// otherwise its deletion can hide loss of a declared promise's coverage.
func TestAnUnclaimedTestFileIsNamed(t *testing.T) {
	root := project(t, "")
	claimRecord(t, root, "ui/panel/data", "")
	writeFile(t, filepath.Join(root, "src", "data.spec.ts"), "it('works', () => {})\n")

	got := findings(lintProject(t, root), "unclaimed-tests")
	if len(got) != 1 {
		t.Fatalf("want one directory finding, got %+v", got)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("an unclaimed test is a question, not a defect: %+v", got[0])
	}
	if !strings.Contains(got[0].Message, "data.spec.ts") {
		t.Errorf("the finding does not name the file: %q", got[0].Message)
	}
	if got[0].Path != "src" {
		t.Errorf("the finding is anchored at %q, want the directory", got[0].Path)
	}
}

// The other half, and the case that stops "report everything" from passing
// the one above.
func TestATestARecordNamesIsNotReported(t *testing.T) {
	root := project(t, "")
	claimRecord(t, root, "ui/panel/data", `  unit_tests:
    - id: src/data.spec.ts
      proves: [it-works]
    - id: src/other.spec.ts::the case
      proves: [it-works]
`)
	writeFile(t, filepath.Join(root, "src", "data.spec.ts"), "it('works', () => {})\n")
	writeFile(t, filepath.Join(root, "src", "other.spec.ts"), "it('works', () => {})\n")

	if got := findings(lintProject(t, root), "unclaimed-tests"); len(got) != 0 {
		t.Fatalf("both tests are claimed, got %+v", got)
	}
}

// One directory is one finding.
func TestUnclaimedTestsAreGroupedByDirectory(t *testing.T) {
	root := project(t, "")
	claimRecord(t, root, "ui/panel/data", "")
	for _, name := range []string{"a.spec.ts", "b.spec.ts", "c.spec.ts"} {
		writeFile(t, filepath.Join(root, "src", "deep", name), "it('works', () => {})\n")
	}

	got := findings(lintProject(t, root), "unclaimed-tests")
	if len(got) != 1 {
		t.Fatalf("three files in one directory are one finding, got %d: %+v", len(got), got)
	}
	msg := messagesOf(got)[0]
	for _, name := range []string{"a.spec.ts", "b.spec.ts", "c.spec.ts"} {
		if !strings.Contains(msg, name) {
			t.Errorf("the finding drops %s: %q", name, msg)
		}
	}
	if !strings.Contains(msg, "3 test file(s)") {
		t.Errorf("the finding does not say how many: %q", msg)
	}
}

// A jest snapshot is not a test.
func TestASnapshotIsNotATest(t *testing.T) {
	root := project(t, "")
	claimRecord(t, root, "ui/panel/data", `  unit_tests:
    - id: src/data.spec.tsx
      proves: [it-works]
`)
	writeFile(t, filepath.Join(root, "src", "data.spec.tsx"), "it('works', () => {})\n")
	writeFile(t, filepath.Join(root, "src", "__snapshots__", "data.spec.tsx.snap"), "exports[`works 1`] = `x`;\n")

	if got := findings(lintProject(t, root), "unclaimed-tests"); len(got) != 0 {
		t.Fatalf("a snapshot is the imprint of a test, not a test, got %+v", got)
	}
}

func TestAGoFixtureWithNoTestFunctionIsNotATest(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "src", "fake_server_test.go"),
		"package src\n\nfunc newFakeServer() string { return \"\" }\n")

	if got := findings(lintProject(t, root), "unclaimed-tests"); len(got) != 0 {
		t.Fatalf("a fixture declares no promise and cannot be claimed, got %+v", got)
	}

	// And the rule does not swallow the directory: a real test beside it is
	// still reported.
	writeFile(t, filepath.Join(root, "src", "real_test.go"),
		"package src\n\nimport \"testing\"\n\nfunc TestSomething(t *testing.T) {}\n")

	got := findings(lintProject(t, root), "unclaimed-tests")
	if len(got) != 1 {
		t.Fatalf("want the real test reported, got %+v", got)
	}
	if !strings.Contains(got[0].Message, "real_test.go") {
		t.Errorf("the finding should name the real test: %q", got[0].Message)
	}
	if strings.Contains(got[0].Message, "fake_server_test.go") {
		t.Errorf("the finding should not name the fixture: %q", got[0].Message)
	}
}
