// Fixtures here contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file
package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec §7 requires an error for a verification naming a nonexistent test file;
// those scenarios must not count as proven.
func TestAVerificationNamingAMissingFileIsAnError(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Given a panel
        Then data loads
`,
		`  unit_tests:
    - id: src/data.spec.ts
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	errs := errorsOf(findings(lintProject(t, root), "verification-targets"))
	if len(errs) != 1 {
		t.Fatalf("want one verification-targets error, got %+v", errs)
	}
	if !strings.Contains(errs[0].Message, "src/data.spec.ts") {
		t.Errorf("the finding must name the file it could not find: %q", errs[0].Message)
	}
	if errs[0].ID != "ui/panel/data" {
		t.Errorf("the finding must name the record, got %q", errs[0].ID)
	}
}

// The same record with the file actually written.
func TestAVerificationNamingAFileThatExistsIsClean(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
`,
		`  unit_tests:
    - id: src/data.spec.ts
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")
	writeFile(t, filepath.Join(root, "src", "data.spec.ts"), "it('loads', () => {})\n")

	if got := findings(lintProject(t, root), "verification-targets"); len(got) != 0 {
		t.Fatalf("a verification pointing at a real file must be clean, got %+v", got)
	}
}

func TestAnIdThatIsNotAPathIsLeftAlone(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
    - id: data-ships
      statement: |
        Then data ships
`,
		`  unit_tests:
    - id: api::promo::it_cap_message_shown
      proves: [data-loads]
  integration_tests:
    - id: "facet-self-check / manifests / the dispatcher asks for the right extension"
      proves: [data-ships]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	if got := findings(lintProject(t, root), "verification-targets"); len(got) != 0 {
		t.Fatalf("a non-path id must not be reported as a missing file, got %+v", got)
	}
}

// A path with a test or job name after it still names a file, and the file is
// what gets checked.
func TestAPathWithATestNameAfterItIsResolvedToTheFile(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
    - id: data-ships
      statement: |
        Then data ships
    - id: data-goes
      statement: |
        Then data goes
`,
		`  unit_tests:
    - id: tests/billing/test_cap.py::test_cap_applied
      proves: [data-loads]
  e2e_tests:
    - id: .github/workflows/check.yml:fresh-adoption
      proves: [data-ships]
    - id: .github/workflows/gone.yml:fresh-adoption
      proves: [data-goes]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")
	writeFile(t, filepath.Join(root, "tests", "billing", "test_cap.py"), "def test_cap_applied(): pass\n")
	writeFile(t, filepath.Join(root, ".github", "workflows", "check.yml"), "on: push\n")

	errs := errorsOf(findings(lintProject(t, root), "verification-targets"))
	if len(errs) != 1 {
		t.Fatalf("want exactly the one missing workflow reported, got %+v", errs)
	}
	if !strings.Contains(errs[0].Message, "gone.yml") {
		t.Errorf("the wrong entry was reported: %q", errs[0].Message)
	}
}

// One missing file is one finding, however many scenarios it claims.
func TestOneMissingFileIsReportedOnce(t *testing.T) {
	root := project(t, "")
	provesRecord(t, root, "ui/panel/data",
		`    - id: data-loads
      statement: |
        Then data loads
    - id: data-ships
      statement: |
        Then data ships
    - id: data-goes
      statement: |
        Then data goes
`,
		`  unit_tests:
    - id: src/data.spec.ts
      proves: [data-loads, data-ships, data-goes]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")

	errs := errorsOf(findings(lintProject(t, root), "verification-targets"))
	if len(errs) != 1 {
		t.Fatalf("one missing file must be one finding, got %d: %+v", len(errs), errs)
	}
}

// errorsOf keeps the error-severity findings, since lintProject returns a
// record's warnings alongside them.
func errorsOf(all []Finding) []Finding {
	var errs []Finding
	for _, f := range all {
		if f.Severity == SeverityError {
			errs = append(errs, f)
		}
	}
	return errs
}

// Named mutation: remove the uninitialized-submodule warning branch from
// lintVerificationTargets. The genuine deinitialized fixture becomes an error.
func TestVerificationInAnUninitializedSubmoduleWarns(t *testing.T) {
	root, module := verificationSubmoduleProject(t)
	git(t, root, "submodule", "deinit", "-f", "--", module)
	if _, err := os.Stat(filepath.Join(root, module, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fixture must have no checkout metadata: %v", err)
	}
	got := findings(lintProject(t, root), "verification-targets")
	if len(got) != 1 || got[0].Severity != SeverityWarning ||
		!strings.Contains(got[0].Message, "uninitialized submodule") ||
		!strings.Contains(got[0].Hint, "git submodule update --init") ||
		!strings.Contains(got[0].Hint, module) {
		t.Fatalf("want one actionable submodule warning, got %+v", got)
	}
}

// Mutation: compare the lexical project root with Git's resolved top-level.
// The same uninitialized checkout then becomes an error through a symlink.
func TestVerificationInAnUninitializedSubmoduleWarnsThroughASymlinkRoot(t *testing.T) {
	root, module := verificationSubmoduleProject(t)
	git(t, root, "submodule", "deinit", "-f", "--", module)
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	got := findings(lintProject(t, link), "verification-targets")
	if len(got) != 1 || got[0].Severity != SeverityWarning ||
		!strings.Contains(got[0].Hint, "'"+module+"'") ||
		strings.Contains(got[0].Hint, "../") {
		t.Fatalf("want the same warning with a project-relative hint, got %+v", got)
	}
}

// Named mutation: consider every gitlink uninitialized, even with checkout
// metadata. The missing-file assertion becomes a warning and fails.
func TestVerificationInAnInitializedSubmoduleStillChecksFiles(t *testing.T) {
	root, module := verificationSubmoduleProject(t)
	if _, err := os.Stat(filepath.Join(root, module, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := findings(lintProject(t, root), "verification-targets"); len(got) != 0 {
		t.Fatalf("a checked-out test is clean: %+v", got)
	}
	if err := os.Remove(filepath.Join(root, module, "test.spec.ts")); err != nil {
		t.Fatal(err)
	}
	got := findings(lintProject(t, root), "verification-targets")
	if len(got) != 1 || got[0].Severity != SeverityError {
		t.Fatalf("a deleted test in an initialized submodule remains an error: %+v", got)
	}
}

// Named mutation: trust .gitmodules declarations without checking Git's
// gitlinks. A stale declaration then hides this ordinary missing-file error.
func TestAGitmodulesDeclarationAloneDoesNotExcuseAMissingTest(t *testing.T) {
	root, module := verificationSubmoduleProject(t)
	git(t, root, "rm", "-f", "--", module)
	writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"stale\"]\n path = "+module+"\n url = ../gone\n")
	got := findings(lintProject(t, root), "verification-targets")
	if len(got) != 1 || got[0].Severity != SeverityError {
		t.Fatalf("a declaration without a gitlink remains an error: %+v", got)
	}
}

// Named mutation: replace the path-containment check with a string prefix.
// A sibling directory then becomes a warning. Parent traversal is also checked.
func TestSubmoduleWarningDoesNotHideNearbyMissingFiles(t *testing.T) {
	for _, target := range []string{"vendor/ui-other/missing.spec.ts", "vendor/ui/../missing.spec.ts"} {
		t.Run(target, func(t *testing.T) {
			root, module := verificationSubmoduleProject(t)
			git(t, root, "submodule", "deinit", "-f", "--", module)
			rec := filepath.Join(root, "docs/facets/features/ui/panel/data.yaml")
			content, err := os.ReadFile(rec)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, rec, strings.ReplaceAll(string(content), "vendor/ui/test.spec.ts", target))
			got := findings(lintProject(t, root), "verification-targets")
			if len(got) != 1 || got[0].Severity != SeverityError {
				t.Fatalf("a missing file outside the module remains an error: %+v", got)
			}
		})
	}
}

// Git itself writes and parses the configuration; the quoted section name
// exercises names that a hand-written .gitmodules line parser can misread.
func verificationSubmoduleProject(t *testing.T) (string, string) {
	t.Helper()
	root := project(t, "")
	upstream := t.TempDir()
	git(t, upstream, "init", "-q")
	git(t, upstream, "config", "user.name", "Fixture")
	git(t, upstream, "config", "user.email", "fixture@example.invalid")
	writeFile(t, filepath.Join(upstream, "test.spec.ts"), "it('loads', () => {})\n")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "fixture")
	git(t, root, "init", "-q")
	module := "vendor/ui"
	git(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "--name", "ui \"quoted\"", upstream, module)
	if !strings.Contains(git(t, root, "ls-files", "--stage", "--", module), "160000") {
		t.Fatal("fixture must contain a real gitlink")
	}
	provesRecord(t, root, "ui/panel/data", `    - id: data-loads
      statement: |
        Then data loads
`, `  unit_tests:
    - id: vendor/ui/test.spec.ts
      proves: [data-loads]
`)
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n// @facet-role: primary\n")
	return root, module
}
