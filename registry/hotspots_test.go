// Fixtures here contain example @facet: tags; the file exercises the check
// rather than implementing it, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// hotspotFile builds one SourceFile without touching the disk: the check
// consumes what the scan already gathered, so the fixture is that, not a tree.
func hotspotFile(root, rel string, loc int, anchored, optedOut bool) SourceFile {
	return SourceFile{
		Path:     filepath.Join(root, filepath.FromSlash(rel)),
		Loc:      loc,
		Anchored: anchored,
		OptedOut: optedOut,
	}
}

func hotspotPaths(found []UnanchoredFile) []string {
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.Path
	}
	return out
}

// The case this check exists for: a large file implementing a whole behaviour,
// carrying no anchor while the records describing it point elsewhere. Every
// other check asks its question from the registry outward and
// therefore cannot see a file no record mentions.
func TestUnanchoredHotspotIsReportedAmongAnchoredNeighbours(t *testing.T) {
	root := "/repo"
	// 1221 unanchored lines against a directory that is otherwise anchored.
	files := []SourceFile{
		hotspotFile(root, "cmd/app/lint.go", 1400, true, false),
		hotspotFile(root, "cmd/app/drift.go", 900, true, false),
		hotspotFile(root, "cmd/app/hook.go", 1221, false, false),
	}

	found := UnanchoredHotspots(root, files, nil)
	if len(found) != 1 {
		t.Fatalf("want the one unanchored file, got %v", hotspotPaths(found))
	}
	if found[0].Path != "cmd/app/hook.go" {
		t.Fatalf("want cmd/app/hook.go, got %q", found[0].Path)
	}
	if found[0].Loc != 1221 {
		t.Errorf("Loc = %d, want 1221", found[0].Loc)
	}
	// The share is the whole argument — without it "this file has no anchor" is
	// true of most files in most repositories.
	if found[0].DirAnchoredPercent != 2300*100/3521 {
		t.Errorf("DirAnchoredPercent = %d, want the directory's real share", found[0].DirAnchoredPercent)
	}
}

// The check is silent in directories with no anchored practice to compare
// against; adoption alone must not flag every source file.
func TestUnanchoredHotspotsAreSilentWhereNothingIsAnchored(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		hotspotFile(root, "src/a.go", 500, false, false),
		hotspotFile(root, "src/b.go", 500, false, false),
		hotspotFile(root, "src/c.go", 500, true, false),
	}

	if found := UnanchoredHotspots(root, files, nil); len(found) != 0 {
		t.Fatalf("a barely-anchored directory must stay quiet, got %v", hotspotPaths(found))
	}
}

// A file that declared '@facet-ignore-file' has said in writing that it is a
// fixture rather than an implementation. Reporting it would make the
// declaration itself look like the omission, and in a typical tree most such
// files are opted-out test fixtures.
func TestADeclaredOptOutIsNotAnOmission(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		hotspotFile(root, "pkg/x/impl.go", 900, true, false),
		hotspotFile(root, "pkg/x/fixtures.go", 400, false, true),
	}

	if found := UnanchoredHotspots(root, files, nil); len(found) != 0 {
		t.Fatalf("an opted-out file must not be a finding, got %v", hotspotPaths(found))
	}
}

// Tests are neither hotspot candidates nor denominator evidence: including
// them would lower the measured share of documented production sources.
func TestTestCodeIsNeitherCandidateNorEvidence(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		hotspotFile(root, "pkg/x/impl.go", 300, true, false),
		hotspotFile(root, "pkg/x/impl_test.go", 2000, false, false),
		hotspotFile(root, "pkg/x/helper.go", 150, false, false),
	}

	found := UnanchoredHotspots(root, files, nil)
	if len(found) != 1 || found[0].Path != "pkg/x/helper.go" {
		t.Fatalf("want only the untested helper reported, got %v", hotspotPaths(found))
	}
	// 2000 lines of test in the denominator would have put the directory at 12%
	// anchored and silenced the check entirely.
	if found[0].DirAnchoredPercent != 300*100/450 {
		t.Errorf("DirAnchoredPercent = %d — tests must not dilute the share", found[0].DirAnchoredPercent)
	}
}

// The neighbourhood is the directory, not the repository. A repository-wide
// share averages a documented core together with a vendored tree and lands on a
// number that describes neither.
func TestTheNeighbourhoodIsTheDirectory(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		// A thoroughly documented package.
		hotspotFile(root, "internal/core/a.go", 900, true, false),
		hotspotFile(root, "internal/core/b.go", 300, false, false),
		// An undocumented one, big enough to drag a repository-wide share down.
		hotspotFile(root, "vendor/lib/x.go", 5000, false, false),
		hotspotFile(root, "vendor/lib/y.go", 5000, false, false),
	}

	found := UnanchoredHotspots(root, files, nil)
	if len(found) != 1 || found[0].Path != "internal/core/b.go" {
		t.Fatalf("want the documented package judged on its own, got %v", hotspotPaths(found))
	}
}

// Files below the size floor are excluded as helpers; the check targets
// substantial unanchored implementation rather than every small file.
func TestASmallFileIsNotAFeature(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		hotspotFile(root, "pkg/x/impl.go", 900, true, false),
		hotspotFile(root, "pkg/x/tiny.go", hotspotMinLines-1, false, false),
		hotspotFile(root, "pkg/x/big.go", hotspotMinLines, false, false),
	}

	found := UnanchoredHotspots(root, files, nil)
	if len(found) != 1 || found[0].Path != "pkg/x/big.go" {
		t.Fatalf("want only the file at or above the floor, got %v", hotspotPaths(found))
	}
}

// Hotspots are sorted by descending file size so the largest undocumented
// files appear first.
func TestUnanchoredHotspotsAreRankedBySize(t *testing.T) {
	root := "/repo"
	files := []SourceFile{hotspotFile(root, "pkg/x/impl.go", 5000, true, false)}
	for i, loc := range []int{150, 900, 400} {
		files = append(files, hotspotFile(root, fmt.Sprintf("pkg/x/f%d.go", i), loc, false, false))
	}

	got := strings.Join(hotspotPaths(UnanchoredHotspots(root, files, nil)), " ")
	want := "pkg/x/f1.go pkg/x/f2.go pkg/x/f0.go"
	if got != want {
		t.Fatalf("ranking = %q, want %q", got, want)
	}
}

// A custom classifier reaches this check too, so a project whose tests live
// somewhere the default heuristic does not know does not get its whole test
// suite reported as undocumented features.
func TestUnanchoredHotspotsHonourACustomTestClassifier(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		hotspotFile(root, "pkg/x/impl.go", 900, true, false),
		hotspotFile(root, "harness/parity.go", 400, false, false),
		hotspotFile(root, "pkg/x/helper.go", 400, false, false),
	}
	isTest := func(rel string) bool { return strings.HasPrefix(rel, "harness/") }

	found := UnanchoredHotspots(root, files, isTest)
	if len(found) != 1 || found[0].Path != "pkg/x/helper.go" {
		t.Fatalf("the custom classifier must exclude the harness, got %v", hotspotPaths(found))
	}
}
