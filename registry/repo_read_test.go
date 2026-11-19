// Fixtures here are example ADR files and paths, so this file demonstrates
// syntax rather than implementing anything and opts out of scanning.
// @facet-ignore-file

package facet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func adrDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "adr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		mustWrite(t, filepath.Join(dir, name), body)
	}
	return dir
}

const sampleADR = `# ADR 0001 — Title

**Status:** accepted
**Date:** 2026-09-19

---

## 1. Decision

Text.
`

func TestReadRepoFileReadsAFileInsideTheDirectory(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})
	body, err := ReadRepoFile(dir, "0001-title.md", ".md")
	if err != nil {
		t.Fatalf("ReadRepoFile: %v", err)
	}
	if body != sampleADR {
		t.Errorf("ReadRepoFile returned %q, want the file's own content", body)
	}
}

// The escape a caller-supplied path could attempt, compared after resolution
// rather than as a string prefix.
func TestReadRepoFileRefusesAPathOutsideTheDirectory(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})

	outside := []string{
		"../../.github/workflows/release.yml",
		"../taxonomy.yaml",
		"/etc/passwd",
		"0001-title.md/../../etc/passwd",
	}
	for _, rel := range outside {
		if _, err := ReadRepoFile(dir, rel, ".md"); err == nil {
			t.Errorf("ReadRepoFile(%q) was allowed; it must be refused", rel)
		} else if !errors.Is(err, ErrRepoReadPathEscape) && !errors.Is(err, ErrRepoReadPathGrammar) {
			t.Errorf("ReadRepoFile(%q) refused with %v, want a path refusal", rel, err)
		}
	}
}

func TestReadRepoFileRefusesTheWrongExtension(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})
	if err := os.WriteFile(filepath.Join(dir, "0001-title.yaml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRepoFile(dir, "0001-title.yaml", ".md"); !errors.Is(err, ErrRepoReadWrongExt) {
		t.Errorf("ReadRepoFile of a .yaml file = %v, want ErrRepoReadWrongExt", err)
	}
}

// The symlink case on its own, because it is the one a string prefix passes:
// a name that is inside the directory and resolves outside it.
func TestReadRepoFileRefusesASymlinkOutOfTheDirectory(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})
	escape := filepath.Join(dir, "elsewhere.md")
	target := filepath.Join(filepath.Dir(dir), "secret-target.md")
	mustWrite(t, target, "top secret\n")
	if err := os.Symlink(target, escape); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	if _, err := ReadRepoFile(dir, "elsewhere.md", ".md"); !errors.Is(err, ErrRepoReadPathEscape) {
		t.Errorf("ReadRepoFile of a symlink out = %v, want ErrRepoReadPathEscape", err)
	}
}

func TestListRepoFilesListsOnlyTheDeclaredExtensionSorted(t *testing.T) {
	dir := adrDir(t, map[string]string{
		"0002-second.md": sampleADR,
		"0001-first.md":  sampleADR,
	})
	mustWrite(t, filepath.Join(dir, "note.txt"), "not an ADR\n")

	names, err := ListRepoFiles(dir, ".md")
	if err != nil {
		t.Fatalf("ListRepoFiles: %v", err)
	}
	want := []string{"0001-first.md", "0002-second.md"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("ListRepoFiles = %v, want %v", names, want)
	}
}

func TestListRepoFilesOfAMissingDirectoryIsEmptyNotAnError(t *testing.T) {
	root := t.TempDir()
	names, err := ListRepoFiles(filepath.Join(root, "docs", "adr"), ".md")
	if err != nil {
		t.Fatalf("ListRepoFiles of a missing directory: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("ListRepoFiles of a missing directory = %v, want none", names)
	}
}

// A declared directory can pass the path grammar but escape via a symlink;
// containment must still reject it.
func TestResolveReadDirRefusesASymlinkLeavingTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), "secret")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "adr")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Precondition: through the link, the file really is reachable.
	if _, err := os.Stat(filepath.Join(root, "docs", "adr", "secret.md")); err != nil {
		t.Fatalf("fixture precondition: the link does not reach the outside file: %v", err)
	}
	// Mutation: drop the withinResolved check in ResolveReadDir → red.
	if _, err := ResolveReadDir(root, "docs/adr"); !errors.Is(err, ErrRepoReadDirInvalid) {
		t.Fatalf("ResolveReadDir followed a symlink out of the root: %v", err)
	}
}

func TestResolveReadDirAcceptsAPlainDirectory(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveReadDir(root, "docs/adr")
	if err != nil {
		t.Fatalf("ResolveReadDir: %v", err)
	}
	if got != filepath.Join(root, "docs", "adr") {
		t.Errorf("ResolveReadDir = %q", got)
	}
}

// Listing and reading apply the same acceptance rules, including README.md;
// an offered file the reader rejects breaks the view.
func TestListRepoFilesOffersOnlyWhatReadRepoFileAccepts(t *testing.T) {
	dir := adrDir(t, map[string]string{
		"0001-title.md":   sampleADR,
		"README.md":       "# ADRs",
		"ADR-TEMPLATE.md": "# Template",
		".draft.md":       "hidden",
	})
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("fixture precondition: README.md was not written: %v", err)
	}
	names, err := ListRepoFiles(dir, ".md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := ReadRepoFile(dir, name, ".md"); err != nil {
			t.Errorf("ListRepoFiles offered %q, which ReadRepoFile refuses: %v", name, err)
		}
	}
	if len(names) != 1 || names[0] != "0001-title.md" {
		t.Errorf("ListRepoFiles = %v, want [0001-title.md]", names)
	}
}

// ReadRepoFile refuses files above its size bound rather than allocating
// an unbounded read.
func TestReadRepoFileRefusesAFileAboveTheLimit(t *testing.T) {
	big := make([]byte, RepoReadMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	dir := adrDir(t, map[string]string{"0001-large.md": string(big)})
	if _, err := ReadRepoFile(dir, "0001-large.md", ".md"); !errors.Is(err, ErrRepoReadTooLarge) {
		t.Fatalf("a file above the limit was read: %v", err)
	}
	exact := adrDir(t, map[string]string{"0001-exact.md": string(big[:RepoReadMaxBytes])})
	if _, err := ReadRepoFile(exact, "0001-exact.md", ".md"); err != nil {
		t.Fatalf("a file exactly at the limit was refused: %v", err)
	}
}

// A dangling symlink can pass a nearest-existing-ancestor string check.
// Once its target appears, kernel-enforced containment must still refuse it.
// Mutation: drop the openBeneath check from ListRepoFiles → red (the dangling
// link is listed).
func TestADanglingLinkOutOfTheDirectoryIsNeitherListedNorRead(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})
	outside := filepath.Join(t.TempDir(), "later.md")
	if err := os.Symlink(outside, filepath.Join(dir, "0002-later.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Precondition: the old string check really accepts the dangling link.
	if _, err := ResolveRepoReadPath(dir, "0002-later.md", ".md"); err != nil {
		t.Fatalf("fixture precondition: the path check already refuses the link: %v", err)
	}
	if names, _ := ListRepoFiles(dir, ".md"); len(names) != 1 {
		t.Errorf("ListRepoFiles offered a dangling link out of the directory: %v", names)
	}
	mustWrite(t, outside, "SECRET")
	// Precondition: through the link, the outside file really is readable.
	if got, err := os.ReadFile(filepath.Join(dir, "0002-later.md")); err != nil || string(got) != "SECRET" {
		t.Fatalf("fixture precondition: the link does not reach the outside file: %q %v", got, err)
	}
	if body, err := ReadRepoFile(dir, "0002-later.md", ".md"); err == nil {
		t.Fatalf("ReadRepoFile followed a link out of the directory: %q", body)
	}
}

// A /proc/self/fd link can still open a deleted file outside the root even
// when EvalSymlinks fails on its deleted suffix. The containment check
// must refuse that link rather than treating resolution failure as inside.
func TestAMagicLinkToAnOpenFileOutsideIsRefused(t *testing.T) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("no /proc on this system")
	}
	dir := adrDir(t, nil)
	secret := filepath.Join(t.TempDir(), "secret.md")
	mustWrite(t, secret, "SECRET")
	f, err := os.Open(secret)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Remove(secret); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "0001-a.md")
	if err := os.Symlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Precondition: the magic link really reaches the deleted file.
	if got, err := os.ReadFile(link); err != nil || string(got) != "SECRET" {
		t.Fatalf("fixture precondition: the magic link does not reach the file: %q %v", got, err)
	}
	if body, err := ReadRepoFile(dir, "0001-a.md", ".md"); err == nil {
		t.Fatalf("ReadRepoFile followed a magic link out of the directory: %q", body)
	}
}

// ResolveReadDir refuses a declared directory that is a dangling symlink
// outside the repository; unresolved links do not satisfy containment.
func TestResolveReadDirRefusesADanglingLinkOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "later")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "adr")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveReadDir(root, "docs/adr"); !errors.Is(err, ErrRepoReadDirInvalid) {
		t.Fatalf("ResolveReadDir accepted a dangling link out of the root: %v", err)
	}
}

// A declared directory that does not exist is no refusal: it lists nothing.
func TestResolveReadDirAcceptsAMissingDirectory(t *testing.T) {
	if _, err := ResolveReadDir(t.TempDir(), "docs/adr"); err != nil {
		t.Fatalf("ResolveReadDir refused a directory that simply does not exist: %v", err)
	}
}
