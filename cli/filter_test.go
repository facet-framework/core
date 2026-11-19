// Fixtures here contain example @facet: tags; the file exercises the filter
// against a real git repository rather than implementing a feature, so it
// opts out of scanning.
// @facet-ignore-file
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	facetBinaryOnce sync.Once
	facetBinaryPath string
	facetBinaryErr  error
)

// facetBinary builds the real binary once per test run. The filter is
// exercised by git itself, which spawns whatever command the clone's config
// names — that has to be a file on disk, not a function in this process.
func facetBinary(t *testing.T) string {
	t.Helper()
	facetBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "facet-filter-bin-")
		if err != nil {
			facetBinaryErr = err
			return
		}
		facetBinaryPath = filepath.Join(dir, "facet")
		build := exec.Command("go", "build", "-o", facetBinaryPath, cliCommandPackage(t))
		if out, err := build.CombinedOutput(); err != nil {
			facetBinaryErr = err
			t.Logf("go build: %s", out)
		}
	})
	if facetBinaryErr != nil {
		t.Skipf("cannot build the facet binary here: %v", facetBinaryErr)
	}
	return facetBinaryPath
}

// git runs one git command in dir and fails the test on error, returning
// stdout. Skips the test when git itself is unavailable, as drift_test does.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			t.Skipf("git is unavailable here: %v", err)
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func facetRun(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("facet %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

const (
	committedFile = "// @facet: a/b/committed\n// @facet-role: primary\npackage x\n\nfunc Keep() {}\n\nfunc Move() {}\n"
	privateBlock  = "// @facet: a/b/private\n// @facet-role: related\n"
	annotatedFile = "// @facet: a/b/committed\n// @facet-role: primary\npackage x\n\nfunc Keep() {}\n\n" + privateBlock + "func Move() {}\n"
)

// twoClones sets up an origin with one committed file, a clone that installs
// the filter, and a second clone that never does — the upstream, whose edits
// arrive the way real ones do, through fetch and merge.
func twoClones(t *testing.T) (bin, mine, theirs string) {
	t.Helper()
	bin = facetBinary(t)
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	git(t, base, "init", "-q", "--bare", origin)
	mine = filepath.Join(base, "mine")
	theirs = filepath.Join(base, "theirs")
	for _, d := range []string{mine, theirs} {
		git(t, base, "clone", "-q", origin, d)
		git(t, d, "config", "user.email", "t@t")
		git(t, d, "config", "user.name", "t")
	}
	write(t, filepath.Join(theirs, "x.go"), committedFile)
	git(t, theirs, "add", "x.go")
	git(t, theirs, "commit", "-qm", "init")
	git(t, theirs, "push", "-q", "origin", "HEAD:master")
	git(t, mine, "pull", "-q", "origin", "master")
	facetRun(t, bin, mine, "filter", "install", "--root", mine)
	return bin, mine, theirs
}

func upstreamEdit(t *testing.T, theirs, content, message string) {
	t.Helper()
	write(t, filepath.Join(theirs, "x.go"), content)
	git(t, theirs, "commit", "-qam", message)
	git(t, theirs, "push", "-q", "origin", "HEAD:master")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// a-private-anchor-never-reaches-a-blob, a-committed-anchor-passes-through,
// the-working-copy-round-trips: the index, the commit and the remote never see
// the private block; the committed one is untouched; a checkout brings the
// private one back.
func TestPrivateAnchorsNeverReachGit(t *testing.T) {
	bin, mine, _ := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	git(t, mine, "add", "x.go")

	staged := git(t, mine, "show", ":x.go")
	if strings.Contains(staged, "a/b/private") {
		t.Fatal("the private anchor reached the index")
	}
	if !strings.Contains(staged, "a/b/committed") {
		t.Fatal("the committed anchor was stripped")
	}
	if staged != committedFile {
		t.Fatalf("staged content is not the committed file:\n%s", staged)
	}
	// Nothing to commit: the cleaned content equals HEAD.
	if out := git(t, mine, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("after add, status must be clean, got %q", out)
	}
	if !strings.Contains(facetRun(t, bin, mine, "filter", "status", "--root", mine), "a/b/private") {
		t.Fatal("status must list the private anchor")
	}

	// The working copy round-trips through a checkout.
	if err := os.Remove(filepath.Join(mine, "x.go")); err != nil {
		t.Fatal(err)
	}
	git(t, mine, "checkout", "--", "x.go")
	if got := readFile(t, filepath.Join(mine, "x.go")); got != annotatedFile {
		t.Fatalf("checkout did not restore the working copy:\n%s", got)
	}
}

// git judges by size before it consults the filter: a file that just gained a
// private anchor shows as modified while 'git diff' is empty, and 'git pull'
// refuses to run. 'facet filter refresh' clears it.
func TestRefreshMakesStatusAgreeWithDiff(t *testing.T) {
	bin, mine, _ := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)

	if out := git(t, mine, "diff"); strings.TrimSpace(out) != "" {
		t.Fatalf("diff must be empty, the filter removes the only change: %q", out)
	}
	facetRun(t, bin, mine, "filter", "refresh", "--root", mine)
	if out := git(t, mine, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("after refresh, status must agree with diff, got %q", out)
	}
}

// an-anchor-whose-place-moved-follows-it: an upstream edit above the
// declaration arrives through a pull, and the block is still above it.
func TestAPrivateAnchorFollowsItsDeclarationThroughAPull(t *testing.T) {
	bin, mine, theirs := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	facetRun(t, bin, mine, "filter", "refresh", "--root", mine)

	upstreamEdit(t, theirs, "// @facet: a/b/committed\n// @facet-role: primary\npackage x\n\nfunc Keep() {}\n\nfunc Added() {}\n\nfunc Move() {}\n", "add Added")
	git(t, mine, "pull", "-q", "origin", "master")

	got := readFile(t, filepath.Join(mine, "x.go"))
	if !strings.Contains(got, "func Added() {}") {
		t.Fatal("the upstream edit did not arrive")
	}
	if !strings.Contains(got, privateBlock+"func Move() {}") {
		t.Fatalf("the private block did not follow Move:\n%s", got)
	}
	if out := git(t, mine, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("after the pull, status must be clean, got %q", out)
	}
}

// an-anchor-whose-place-is-gone-is-kept-and-reported, then restored: a rename
// upstream orphans the block — reported, never guessed into the file, kept
// through the next clean — and the name coming back puts it back and clears
// the mark.
func TestAnOrphanedPrivateAnchorIsKeptReportedAndRestored(t *testing.T) {
	bin, mine, theirs := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	facetRun(t, bin, mine, "filter", "refresh", "--root", mine)

	upstreamEdit(t, theirs, strings.Replace(committedFile, "func Move()", "func Renamed()", 1), "rename")
	git(t, mine, "pull", "-q", "origin", "master")

	if strings.Contains(readFile(t, filepath.Join(mine, "x.go")), "a/b/private") {
		t.Fatal("an orphaned block must not be guessed into the file")
	}
	status := facetRun(t, bin, mine, "filter", "status", "--root", mine)
	if !strings.Contains(status, "no place in the current file") {
		t.Fatalf("status must report the orphan:\n%s", status)
	}
	// A clean now sees no such lines in the working copy. The orphan survives.
	git(t, mine, "add", "x.go")
	if !strings.Contains(facetRun(t, bin, mine, "filter", "status", "--root", mine), "a/b/private") {
		t.Fatal("the orphan was dropped by the clean that followed")
	}

	upstreamEdit(t, theirs, committedFile, "restore")
	git(t, mine, "pull", "-q", "origin", "master")
	if got := readFile(t, filepath.Join(mine, "x.go")); got != annotatedFile {
		t.Fatalf("the restored declaration must bring the block back:\n%s", got)
	}
	if strings.Contains(facetRun(t, bin, mine, "filter", "status", "--root", mine), "no place") {
		t.Fatal("a block that found its place again must not stay marked orphaned")
	}
}

// install-touches-nothing-in-the-repository: the three things install writes
// are all inside the clone, and 'git status' has nothing to say afterwards.
// uninstall removes what install wrote and nothing else.
func TestInstallAndUninstallTouchOnlyTheClone(t *testing.T) {
	bin, mine, _ := twoClones(t)

	if out := git(t, mine, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("install must leave the repository untouched, got %q", out)
	}
	attrs := strings.TrimSpace(git(t, mine, "rev-parse", "--git-path", "info/attributes"))
	if !filepath.IsAbs(attrs) {
		attrs = filepath.Join(mine, attrs)
	}
	if !strings.Contains(readFile(t, attrs), filterAttributeLine) {
		t.Fatal("the attributes line is missing")
	}
	if !strings.Contains(git(t, mine, "config", "--get", "filter.facet.required"), "true") {
		t.Fatal("required must be set: a skipped filter commits every private anchor")
	}
	if _, err := os.Stat(filepath.Join(mine, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatal("install must not write a .gitattributes into the tree")
	}

	// Idempotent.
	facetRun(t, bin, mine, "filter", "install", "--root", mine)
	if n := strings.Count(readFile(t, attrs), filterAttributeLine); n != 1 {
		t.Fatalf("a second install must not duplicate the attributes line, found %d", n)
	}

	facetRun(t, bin, mine, "filter", "uninstall", "--root", mine)
	if strings.Contains(readFile(t, attrs), filterAttributeLine) {
		t.Fatal("uninstall must remove the attributes line")
	}
	if out, _ := exec.Command("git", "-C", mine, "config", "--get", "filter.facet.clean").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatal("uninstall must remove the filter config")
	}
}

// smudge-never-fails-a-checkout: with no sidecar for the path, and with a
// sidecar that will not parse, the content goes through as git handed it.
func TestSmudgeNeverFailsACheckout(t *testing.T) {
	bin, mine, _ := twoClones(t)
	gitDir := filepath.Join(mine, ".git")
	sidecar := filepath.Join(gitDir, "facet", "anchors", "x.go.yaml")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sidecar, "blocks: [\n")

	cmd := exec.Command(bin, "filter", "smudge", "x.go")
	cmd.Dir = mine
	cmd.Stdin = strings.NewReader(committedFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("a broken sidecar must not fail the smudge: %v", err)
	}
	if string(out) != committedFile {
		t.Fatalf("on trouble the content must pass through unchanged, got:\n%s", out)
	}
}

// A linked worktree shares the clone's config and attributes, so it inherits
// the filter without a second install. It has to inherit the anchors too, or
// it would read as a clone where they had all been lost: the sidecar lives in
// the shared git directory, and what one worktree adds the other checks out.
func TestPrivateAnchorsAreSharedAcrossWorktrees(t *testing.T) {
	bin, mine, _ := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	git(t, mine, "add", "x.go")

	wt := filepath.Join(filepath.Dir(mine), "feature")
	git(t, mine, "worktree", "add", "-q", wt, "-b", "feature")

	if got := readFile(t, filepath.Join(wt, "x.go")); got != annotatedFile {
		t.Fatalf("the worktree must check out the private anchors too:\n%s", got)
	}
	if !strings.Contains(git(t, wt, "check-attr", "filter", "x.go"), "facet") {
		t.Fatal("the worktree must inherit the filter attribute")
	}

	// An anchor added in the worktree is private there and known to the
	// main worktree's next checkout.
	other := "// @facet: a/b/committed\n// @facet-role: primary\npackage x\n\n// @facet: c/d/from-worktree\n// @facet-role: related\nfunc Keep() {}\n\n" + privateBlock + "func Move() {}\n"
	write(t, filepath.Join(wt, "x.go"), other)
	git(t, wt, "add", "x.go")
	if strings.Contains(git(t, wt, "show", ":x.go"), "from-worktree") {
		t.Fatal("the worktree's private anchor reached its index")
	}
	if out := git(t, wt, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("after add, the worktree's status must be clean, got %q", out)
	}
	status := facetRun(t, bin, wt, "filter", "status", "--root", wt)
	if !strings.Contains(status, "from-worktree") || strings.Contains(status, "no place") {
		t.Fatalf("status in the worktree must list both blocks, none orphaned:\n%s", status)
	}
	if err := os.Remove(filepath.Join(mine, "x.go")); err != nil {
		t.Fatal(err)
	}
	git(t, mine, "checkout", "--", "x.go")
	if got := readFile(t, filepath.Join(mine, "x.go")); got != other {
		t.Fatalf("the main worktree must see what the linked one added:\n%s", got)
	}
}

// Through real git: a PHP docblock gains the customary
// separator along with the tags, and neither may reach the index.
func TestADocblockSeparatorNeverReachesGit(t *testing.T) {
	bin, mine, theirs := twoClones(t)
	head := "<?php\n/**\n * Creates the invoice.\n */\nfunction create() {}\n"
	write(t, filepath.Join(theirs, "a.php"), head)
	git(t, theirs, "add", "a.php")
	git(t, theirs, "commit", "-qm", "php")
	git(t, theirs, "push", "-q", "origin", "HEAD:master")
	git(t, mine, "pull", "-q", "origin", "master")

	work := "<?php\n/**\n * Creates the invoice.\n *\n * @facet: billing/invoice/create\n * @facet-role: primary\n */\nfunction create() {}\n"
	write(t, filepath.Join(mine, "a.php"), work)
	git(t, mine, "add", "a.php")
	if staged := git(t, mine, "show", ":a.php"); staged != head {
		t.Fatalf("the index must hold exactly HEAD, got:\n%s", staged)
	}
	if out := git(t, mine, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("status must be clean, got %q", out)
	}
	facetRun(t, bin, mine, "filter", "status", "--root", mine)
	if err := os.Remove(filepath.Join(mine, "a.php")); err != nil {
		t.Fatal(err)
	}
	git(t, mine, "checkout", "--", "a.php")
	if got := readFile(t, filepath.Join(mine, "a.php")); got != work {
		t.Fatalf("checkout must restore the docblock with its separator:\n%s", got)
	}
}
