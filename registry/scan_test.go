// The fixtures below anchor features that exist only in a temporary
// directory, so this file opts itself out of anchor scanning: @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// A file above the whole-read limit is streamed, not held: during an adoption
// every write hook lints and every lint scans, so a bundle or lockfile held
// whole by each of them costs memory per agent. Streaming must answer the same
// questions the whole read answers — exact line count, anchors in the head,
// the opt-out — and say nothing about an anchor buried past the head.
func TestALargeFileIsStreamedWithTheSameAnswers(t *testing.T) {
	old := scanWholeFileLimit
	scanWholeFileLimit = 1024
	t.Cleanup(func() { scanWholeFileLimit = old })

	var b strings.Builder
	b.WriteString("// @facet: demo/thing/widget\n// @facet-role: primary\n")
	for b.Len() < scanLargeHead+4096 {
		b.WriteString("const filler = 1;\n")
	}
	b.WriteString("// @facet: demo/thing/buried\nlast line without newline")
	path := filepath.Join(t.TempDir(), "big.ts")
	writeFile(t, path, b.String())

	// The premise: the fixture really is above the limit and past the head.
	if int64(b.Len()) <= scanWholeFileLimit || b.Len() <= scanLargeHead {
		t.Fatalf("fixture of %d bytes does not exercise the large path", b.Len())
	}

	anchors, meta, err := scanFileWithLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Count(b.String(), "\n") + 1; meta.Loc != want {
		t.Errorf("line count %d, want %d", meta.Loc, want)
	}
	if len(anchors) == 0 || anchors[0].ID != "demo/thing/widget" || anchors[0].Role != "primary" {
		t.Errorf("the head anchor must be found with its role, got %+v", anchors)
	}
	for _, a := range anchors {
		if a.ID == "demo/thing/buried" {
			t.Errorf("an anchor past the head was read: the file was held whole")
		}
	}
}

// The opt-out sits in a file's first lines, so a large file can still take
// itself out of the scan.
func TestALargeFileCanStillOptOut(t *testing.T) {
	old := scanWholeFileLimit
	scanWholeFileLimit = 1024
	t.Cleanup(func() { scanWholeFileLimit = old })

	path := filepath.Join(t.TempDir(), "gen.ts")
	writeFile(t, path, "// @facet-ignore-file\n// @facet: demo/thing/widget\n"+strings.Repeat("x = 1;\n", 1000))
	anchors, meta, err := scanFileWithLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.OptedOut || len(anchors) != 0 {
		t.Errorf("want an opted-out file with no anchors, got opted=%v anchors=%+v", meta.OptedOut, anchors)
	}
}

// A record anchored only in a dot-directory, at various depths, must be found
// by ScanAnchors, including nested .github, .claude and .config directories.
func TestScanAnchorsFindsAnchorsInDotDirectories(t *testing.T) {
	for _, rel := range []string{
		".github/workflows/probe.yml",
		".claude/hooks/probe.sh",
		".config/probe.yml",
		"src/.hidden/probe.ts",
	} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), "# @facet: demo/ci/pipeline\n# @facet-role: primary\n")

			anchors, err := ScanAnchors(root, ScanOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(anchors) != 1 {
				t.Fatalf("expected 1 anchor under %s, got %d: %+v", rel, len(anchors), anchors)
			}
			want := filepath.Join(root, filepath.FromSlash(rel))
			if anchors[0].Path != want {
				t.Fatalf("expected anchor at %q, got %q", want, anchors[0].Path)
			}
		})
	}
}

// A record anchored only inside
// .github/workflows/ must be found, and facet lint must not report the
// record as an orphan.
func TestScanAnchorsGithubWorkflowsAnchorIsNotReportedAsOrphan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/demo/ci/pipeline.yaml"), `schema_version: "0.1"
id: demo/ci/pipeline
lifecycle: active
intent:
  summary: |
    A demo feature anchored only in a workflow file under .github/.
contract:
  scenarios:
    - id: it-runs
      statement: |
        Given a push
        When CI runs
        Then it passes
lineage:
  owner:
    team: demo
`)
	mustWrite(t, filepath.Join(root, ".github/workflows/ci.yml"), "# @facet: demo/ci/pipeline\n# @facet-role: primary\nname: CI\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}

	rep, err := LintAll(root, LintOptions{Skip: map[string]bool{"schema": true, "knowledge": true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "orphan-records"); len(got) != 0 {
		t.Fatalf("demo/ci/pipeline is anchored in .github/workflows/ci.yml, must not be reported as an orphan: %+v", got)
	}
}

// containsPathComponent reports whether path has dir as one of its
// slash-separated components, anywhere along the path.
func containsPathComponent(path, dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == dir {
			return true
		}
	}
	return false
}

// Edge case, verified rather than assumed: .git must still
// be excluded once IncludeHidden is true, or the walker would read every
// packed object and loose blob under .git looking for a literal "@facet:".
func TestScanAnchorsStillExcludesDotGitDirectory(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)

	// A file that could only ever exist inside .git's own metadata carries an
	// anchor for an entirely different, otherwise-undeclared id. If the
	// walker ever opens it, this id shows up in the scan.
	mustWrite(t, filepath.Join(root, ".git/hooks/pre-commit.sample"), "# @facet: bogus/from/dotgit\n")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range anchors {
		if a.ID == "bogus/from/dotgit" {
			t.Fatalf(".git must never be scanned even with IncludeHidden=true, found: %+v", a)
		}
		if containsPathComponent(a.Path, ".git") {
			t.Fatalf("no anchor may come from inside .git, got path %q", a.Path)
		}
	}
	if len(anchors) != 1 {
		t.Fatalf("expected only the src/checkout.ts anchor, got %d: %+v", len(anchors), anchors)
	}
}

func TestScanAnchorsExcludesVCSDirectoriesWithoutGitAvailable(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/checkout.ts"), "// @facet: payments/checkout/session\n")
	mustWrite(t, filepath.Join(root, ".git/hooks/pre-commit.sample"), "# @facet: bogus/from/dotgit\n")
	mustWrite(t, filepath.Join(root, ".svn/entries"), "@facet: bogus/from/dotsvn\n")
	mustWrite(t, filepath.Join(root, ".hg/store/00changelog.i"), "@facet: bogus/from/dothg\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected only the src/checkout.ts anchor, got %d: %+v", len(anchors), anchors)
	}
	if anchors[0].ID != "payments/checkout/session" {
		t.Fatalf("unexpected anchor found: %+v", anchors[0])
	}
}

// Edge case, verified: a gitignored dot-directory (.venv/ is the typical
// costly example) must still be skipped -
// IgnoreGitIgnore stays false, so .gitignore's own rules keep applying to
// hidden directories exactly as they do to visible ones.
func TestScanAnchorsStillSkipsGitignoredDotDirectory(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
		".gitignore":      ".venv/\n",
	}, nil)
	mustWrite(t, filepath.Join(root, ".venv/lib/site-packages/marker.py"), "# @facet: payments/checkout/session\n")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf(".venv/ is gitignored and must still be skipped, got %d anchors: %+v", len(anchors), anchors)
	}
	if containsPathComponent(anchors[0].Path, ".venv") {
		t.Fatal("the surviving anchor is the one that should have been excluded")
	}
}

// A file whose kind is written in its name rather than its extension.
func TestProseIsRecognisedByNameAsWellAsByExtension(t *testing.T) {
	root := t.TempDir()
	body := "# @facet: demo/ci/pipeline\n# @facet-role: primary\n"
	for _, rel := range []string{"LICENSE", "NOTICE", "COPYING", "bin/facet", "Dockerfile"} {
		mustWrite(t, filepath.Join(root, rel), body)
	}

	_, walked, err := ScanWithFiles(root, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	got := map[string]SourceFile{}
	for _, f := range walked {
		rel, _ := filepath.Rel(root, f.Path)
		got[filepath.ToSlash(rel)] = f
	}

	for _, name := range []string{"LICENSE", "NOTICE", "COPYING"} {
		f, ok := got[name]
		if !ok {
			t.Fatalf("%s was not walked at all", name)
		}
		if f.Anchored || f.Loc != 0 {
			t.Errorf("%s: Anchored=%v Loc=%d, want a file the scan reads as prose", name, f.Anchored, f.Loc)
		}
	}
	// And the ones that only look alike: an extensionless script and a
	// Dockerfile carry their anchors as before.
	for _, name := range []string{"bin/facet", "Dockerfile"} {
		f, ok := got[name]
		if !ok {
			t.Fatalf("%s was not walked at all", name)
		}
		if !f.Anchored {
			t.Errorf("%s: an extensionless file that is not prose must still carry its anchor", name)
		}
	}
}
