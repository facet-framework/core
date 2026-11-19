// Fixtures here are dated repositories, not anchored source.
// @facet-ignore-file
package facet

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Each case builds a real repository with dated commits — a reading of the
// history that finds nothing would otherwise look exactly like one that works.

func TestLastChangedIsTheNewestCommitThatTouchedAPath(t *testing.T) {
	root := newDatedRepo(t)
	commitOnDay(t, root, "src/a.go", "package a\n", "2026-03-01")
	commitOnDay(t, root, "src/b.go", "package b\n", "2026-04-01")
	commitOnDay(t, root, "src/a.go", "package a // edited\n", "2026-06-15")

	got := LastChanged(root)
	// Precondition: the history has both files in it.
	if len(got) < 2 {
		t.Fatalf("expected both paths in the history, got %v", got)
	}
	if got["src/a.go"] != "2026-06-15" {
		t.Fatalf("a.go: want the later edit 2026-06-15, got %q", got["src/a.go"])
	}
	if got["src/b.go"] != "2026-04-01" {
		t.Fatalf("b.go: want 2026-04-01, got %q", got["src/b.go"])
	}
}

// A registry may sit in a subdirectory of a monorepo; the corpus names its
// files relative to that directory, so the history must too.
func TestLastChangedNamesPathsRelativeToTheRegistryRoot(t *testing.T) {
	root := newDatedRepo(t)
	commitOnDay(t, root, "apps/shop/src/cart.go", "package cart\n", "2026-05-02")
	commitOnDay(t, root, "apps/other/x.go", "package x\n", "2026-05-03")

	got := LastChanged(filepath.Join(root, "apps", "shop"))
	if got["src/cart.go"] != "2026-05-02" {
		t.Fatalf("want src/cart.go relative to the registry, got %v", got)
	}
	if _, leaked := got["../other/x.go"]; leaked {
		t.Fatalf("a file outside the registry leaked in: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("only the registry's own file should be named, got %v", got)
	}
}

func TestLastChangedWithoutHistoryClaimsNothing(t *testing.T) {
	if got := LastChanged(t.TempDir()); got != nil {
		t.Fatalf("a directory without git must yield no history, got %v", got)
	}
}

// actions/checkout fetches depth 1: every file would look changed in the one
// commit that exists, and a consumer would sort by a date nobody measured.
func TestLastChangedTreatsAShallowCloneAsNoHistory(t *testing.T) {
	origin := newDatedRepo(t)
	commitOnDay(t, origin, "src/a.go", "package a\n", "2026-05-01")
	commitOnDay(t, origin, "src/b.go", "package b\n", "2026-09-01")

	shallow := filepath.Join(t.TempDir(), "shallow")
	if out, err := exec.Command("git", "clone", "--depth", "1", "--quiet", "file://"+origin, shallow).CombinedOutput(); err != nil {
		t.Fatalf("git clone --depth 1: %v\n%s", err, out)
	}
	// Precondition: the full history does know the days.
	if day := LastChanged(origin)["src/a.go"]; day != "2026-05-01" {
		t.Fatalf("the full clone should know the day, got %q", day)
	}
	if got := LastChanged(shallow); got != nil {
		t.Fatalf("a shallow clone must yield no history, got %v", got)
	}
}
