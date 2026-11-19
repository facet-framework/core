// The fixtures below anchor features that exist only in a temporary
// directory, so this file opts itself out of anchor scanning: @facet-ignore-file

package facet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git is unavailable here: %v (%s)", err, out)
	}
}

// A .git/info/exclude entry excludes nested repositories independently of
// tracked .gitignore and .facetignore rules; the scanner honours it.
func TestGitInfoExcludeExcludesMatchingFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate from any ambient global gitconfig
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("without an exclude entry both anchors are found, got %d", len(anchors))
	}

	mustWrite(t, filepath.Join(root, ".git/info/exclude"), "vendor/\n")
	anchors, err = ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf(".git/info/exclude must exclude vendor/, got %d anchors", len(anchors))
	}
	if anchors[0].Path == filepath.Join(reg.Root, "vendor/bundle.js") {
		t.Fatal("the surviving anchor is the one that should have been excluded")
	}
}

// A root-anchored /build ignore excludes root/build but preserves src/build.
func TestGitInfoExcludeRootAnchoredPatternDoesNotOverExclude(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, reg := gitProject(t, map[string]string{
		"src/build/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "build/bundle.js"), "// @facet: payments/checkout/session\n")
	mustWrite(t, filepath.Join(root, ".git/info/exclude"), "/build\n")

	// Ground truth: git itself must agree src/build/checkout.ts is NOT
	// ignored, or this test is asserting the wrong thing.
	cmd := exec.Command("git", "-C", root, "check-ignore", "-q", "src/build/checkout.ts")
	if err := cmd.Run(); err == nil {
		t.Fatal("test setup is wrong: git itself considers src/build/checkout.ts ignored")
	}

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("a root-anchored /build entry must only exclude the top-level build/, got %d anchors: %+v", len(anchors), anchors)
	}
	if anchors[0].Path != filepath.Join(reg.Root, "src/build/checkout.ts") {
		t.Fatalf("the surviving anchor should be src/build/checkout.ts, got %q", anchors[0].Path)
	}
}

// The XDG-default global exclude file is honoured through
// --exclude-standard even when core.excludesFile is unset.
func TestGitXDGDefaultExcludeFileExcludesMatchingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// git reads $XDG_CONFIG_HOME/git/ignore when that variable is set, and
	// only falls back to $HOME/.config/git/ignore when it is empty. GitHub's
	// Linux runners set it, so without this the file below is never read.
	t.Setenv("XDG_CONFIG_HOME", "")
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")
	mustWrite(t, filepath.Join(home, ".config/git/ignore"), "vendor/\n")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("the XDG-default global exclude file must exclude vendor/, got %d anchors", len(anchors))
	}
}

// core.excludesFile can point anywhere, including entirely outside the
// repository - the same as a real ~/.gitignore_global.
func TestCoreExcludesFileExcludesMatchingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")

	globalExcludes := filepath.Join(home, "gitignore_global")
	mustWrite(t, globalExcludes, "vendor/\n")
	runGit(t, root, "config", "core.excludesFile", "~/gitignore_global")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("core.excludesFile must exclude vendor/, got %d anchors", len(anchors))
	}
}

// Both sources apply together (a union), neither shadows the other.
func TestGitInfoExcludeAndCoreExcludesFileBothApply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "vendor/a.js"), "// @facet: payments/checkout/session\n")
	mustWrite(t, filepath.Join(root, "build/b.js"), "// @facet: payments/checkout/session\n")
	mustWrite(t, filepath.Join(root, ".git/info/exclude"), "vendor/\n")

	globalExcludes := filepath.Join(home, "gitignore_global")
	mustWrite(t, globalExcludes, "build/\n")
	runGit(t, root, "config", "core.excludesFile", "~/gitignore_global")

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("both vendor/ and build/ must be excluded together, got %d anchors", len(anchors))
	}
}

func TestGitInfoExcludeEmptyOrCommentOnlyIsANoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, body := range map[string]string{
		"empty":        "",
		"comment-only": "# nothing to see here\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, reg := gitProject(t, map[string]string{
				"src/checkout.ts": "// @facet: payments/checkout/session\n",
			}, nil)
			mustWrite(t, filepath.Join(root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")
			mustWrite(t, filepath.Join(root, ".git/info/exclude"), body)

			anchors, err := ScanAnchors(reg.Root, ScanOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(anchors) != 2 {
				t.Fatalf("an %s exclude file must not exclude anything, got %d anchors", name, len(anchors))
			}
		})
	}
}

func TestCoreExcludesFileUnsetOrMissingIsSilentlySkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, reg := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")

	// unset: never configured at all.
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("no core.excludesFile configured must not exclude anything, got %d anchors", len(anchors))
	}

	// configured, but the file it names doesn't exist.
	runGit(t, root, "config", "core.excludesFile", "~/does-not-exist")
	anchors, err = ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("a core.excludesFile naming a missing file must not exclude anything, got %d anchors", len(anchors))
	}
}

// A non-git project reports no GitRelevantFiles result, even with global Git exclusions configured.
func TestGitRelevantFilesNonGitProjectIsUnaffected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".gitconfig"), "[core]\n\texcludesFile = ~/gitignore_global\n")
	mustWrite(t, filepath.Join(home, "gitignore_global"), "src/\n")

	reg := newProject(t)
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected the one anchor newProject writes, got %d", len(anchors))
	}
	if _, ok := GitRelevantFiles(reg.Root); ok {
		t.Fatal("a non-git root must report unavailable, not apply an ambient global excludesFile")
	}
}

// git itself missing from PATH degrades the same way a non-git project does
// - silently, not a scan failure.
func TestGitRelevantFilesGitMissingFromPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a real, empty directory - not ""
	reg := newProject(t)
	if _, ok := GitRelevantFiles(reg.Root); ok {
		t.Fatal("git missing from PATH must report unavailable")
	}
	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("a scan must still work with git unavailable, got %d anchors", len(anchors))
	}
}

// New untracked files remain scan candidates. --others --exclude-standard
// includes them while respecting git exclusion rules.
func TestGitRelevantFilesIncludesUntrackedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, reg := gitProject(t, map[string]string{
		"README.md": "placeholder\n",
	}, nil)
	mustWrite(t, filepath.Join(root, "src/checkout.ts"), "// @facet: payments/checkout/session\n")

	status, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(status) == 0 {
		t.Fatal("test setup is wrong: src/checkout.ts should be untracked")
	}

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("an untracked, uncommitted file's anchor must still be found, got %d anchors", len(anchors))
	}
}

// git ls-files collapses embedded repositories without a .gitmodules entry
// into trailing-slash directory entries. Their files must remain scannable.
func TestGitRelevantFilesIncludesFilesInsideAnEmbeddedGitRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, reg := gitProject(t, map[string]string{
		"README.md": "placeholder\n",
	}, nil)

	nested := filepath.Join(root, "vendored")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "init", "-q")
	runGit(t, nested, "config", "user.email", "test@example.invalid")
	runGit(t, nested, "config", "user.name", "Test")
	mustWrite(t, filepath.Join(nested, "src/impl.ts"), "// @facet: payments/checkout/session\n")
	runGit(t, nested, "add", "-A")
	runGit(t, nested, "commit", "-q", "-m", "nested repo, own history")

	// The fixture requires git ls-files to collapse the embedded repository to
	// one directory entry, so scanning must expand that directory entry.
	out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "README.md\nvendored/\n" && string(out) != "vendored/\nREADME.md\n" {
		t.Fatalf("test setup is wrong: expected git to collapse vendored/ to one line, got %q", out)
	}

	anchors, err := ScanAnchors(reg.Root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("an anchor inside an embedded git repo must still be found, got %d anchors", len(anchors))
	}
	if anchors[0].Path != filepath.Join(nested, "src/impl.ts") {
		t.Fatalf("expected the anchor inside vendored/src/impl.ts, got %q", anchors[0].Path)
	}
}

// --git-common-dir (not --git-dir) is what makes this correct from inside a
// linked worktree: .git/info/exclude lives in the *shared* repository
// metadata, not the worktree's own .git/worktrees/<name> directory. git
// ls-files itself already resolves this correctly; this confirms it end to
// end through ScanAnchors.
func TestGitInfoExcludeAppliesFromNestedWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, _ := gitProject(t, map[string]string{
		"src/checkout.ts": "// @facet: payments/checkout/session\n",
	}, nil)
	mustWrite(t, filepath.Join(root, ".git/info/exclude"), "vendor/\n")

	worktree := filepath.Join(t.TempDir(), "wt")
	runGit(t, root, "worktree", "add", "-q", "-b", "wt-branch", worktree)

	// Write the untracked fixture directly into the linked worktree, whose
	// working tree is separate from the main checkout.
	mustWrite(t, filepath.Join(worktree, "vendor/bundle.js"), "// @facet: payments/checkout/session\n")

	anchors, err := ScanAnchors(worktree, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("the main repo's .git/info/exclude must still apply from the worktree, got %d anchors", len(anchors))
	}
	if filepath.Base(filepath.Dir(anchors[0].Path)) == "vendor" {
		t.Fatal("the surviving anchor is the one that should have been excluded")
	}
}
