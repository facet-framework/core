// Fixtures here describe anchored and unanchored files; the file exercises
// the check rather than implementing it, so it opts out of scanning.
// @facet-ignore-file
package facet

import (
	"path/filepath"
	"testing"
)

// undocFile builds one SourceFile without touching the disk: the check
// consumes what the scan already gathered, so the fixture is that, not a tree.
func undocFile(root, rel string, loc int, anchored, optedOut bool) SourceFile {
	return SourceFile{
		Path:     filepath.Join(root, filepath.FromSlash(rel)),
		Loc:      loc,
		Anchored: anchored,
		OptedOut: optedOut,
	}
}

func undocDirs(found []UndocumentedDir) []string {
	out := make([]string, len(found))
	for i, d := range found {
		out[i] = d.Dir
	}
	return out
}

func TestADirectoryWithNoAnchorAtAllIsReported(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		// A documented core, so the repository is past the gate — and in the
		// same file type as the hole, because the project's own anchors are
		// what say which types carry one.
		undocFile(root, "web/reader/Feature.tsx", 4000, true, false),
		undocFile(root, "web/v2/state.tsx", 3000, true, false),
		// And the hole.
		undocFile(root, "web/pages/MainPage.tsx", 709, false, false),
		undocFile(root, "web/pages/MainPage.hoc.tsx", 1555, false, false),
		undocFile(root, "web/pages/Overview.tsx", 391, false, false),
	}

	found := UndocumentedDirectories(root, files, nil)
	if len(found) != 1 {
		t.Fatalf("want the one wholly undocumented directory, got %v", undocDirs(found))
	}
	if found[0].Dir != "web/pages" {
		t.Fatalf("Dir = %q, want web/pages", found[0].Dir)
	}
	if found[0].Loc != 709+1555+391 {
		t.Errorf("Loc = %d, want the directory's whole line count", found[0].Loc)
	}
	if found[0].Files != 3 {
		t.Errorf("Files = %d, want 3", found[0].Files)
	}
	// The largest file is named, because "2655 lines somewhere" is not
	// something a reader can start on and a file name is.
	if found[0].Largest != "web/pages/MainPage.hoc.tsx" {
		t.Errorf("Largest = %q, want the biggest file in the directory", found[0].Largest)
	}
}

// One anchor is enough to take a directory out of this check. Somebody has
// started there, and what is left is the hotspot check's question — a file
// among neighbours — not this one.
func TestADirectoryWithOneAnchorIsNotReported(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "web/reader/Feature.tsx", 4000, true, false),
		undocFile(root, "web/pages/AtlasPage.tsx", 2000, false, false),
		undocFile(root, "web/pages/Overview.tsx", 400, true, false),
	}

	// The precondition: without the one anchored file this directory would be
	// reported, so the case measures the anchor and not the size.
	bare := []SourceFile{files[0], files[1]}
	if found := UndocumentedDirectories(root, bare, nil); len(found) != 1 {
		t.Fatalf("fixture: the directory must be reportable without its anchor, got %v", undocDirs(found))
	}

	if found := UndocumentedDirectories(root, files, nil); len(found) != 0 {
		t.Fatalf("one anchor is enough to take a directory out, got %v", undocDirs(found))
	}
}

// Silent by construction in a project that has not adopted FACET. This is the
// property unanchored-hotspots has through its neighbourhood rule, and the
// reason it can be an unconditional part of lint; a check that asks about a
// whole directory has no neighbourhood to lean on and needs the repository
// share instead.
func TestNothingIsReportedInARepositoryThatHasNotAdoptedFacet(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "src/a.go", 2000, false, false),
		undocFile(root, "src/b.go", 2000, false, false),
		undocFile(root, "lib/c.go", 2000, false, false),
		undocFile(root, "lib/d.go", 400, true, false),
	}

	if found := UndocumentedDirectories(root, files, nil); len(found) != 0 {
		t.Fatalf("a repository below the adoption gate must stay quiet, got %v", undocDirs(found))
	}
}

// A small directory is not a feature nobody wrote down. The floor is the same
// idea as the hotspot check's line floor, one level up.
func TestASmallDirectoryIsNotReported(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "web/v2/state.ts", 4000, true, false),
		undocFile(root, "web/util/format.ts", 40, false, false),
		undocFile(root, "web/util/clamp.ts", 30, false, false),
	}

	if found := UndocumentedDirectories(root, files, nil); len(found) != 0 {
		t.Fatalf("a 70-line directory is not an undocumented feature, got %v", undocDirs(found))
	}
}

// Tests are not features. Counting them would report a directory of test
// helpers as an undocumented capability, and counting them in the repository
// share would make a well-tested project look like one that never adopted
// FACET.
func TestTestCodeIsNeitherCandidateNorEvidenceForADirectory(t *testing.T) {
	root := "/repo"
	isTest := func(rel string) bool { return rel == "web/tests/big.go" }
	files := []SourceFile{
		undocFile(root, "src/core/lint.go", 4000, true, false),
		undocFile(root, "web/tests/big.go", 3000, false, false),
	}

	if found := UndocumentedDirectories(root, files, isTest); len(found) != 0 {
		t.Fatalf("a directory of tests is not an undocumented feature, got %v", undocDirs(found))
	}
}

// A file that opted out has said in writing that it is not an implementation.
// Reporting the directory it sits in would make the declaration itself look
// like the omission.
func TestAnOptedOutDirectoryIsNotReported(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "web/v2/state.ts", 4000, true, false),
		undocFile(root, "web/fixtures/corpus.ts", 2000, false, true),
	}

	if found := UndocumentedDirectories(root, files, nil); len(found) != 0 {
		t.Fatalf("a directory that opted out is not an omission, got %v", undocDirs(found))
	}
}

// Largest first, so a reader who acts on one line acts on the biggest hole.
func TestTheBiggestUndocumentedDirectoryComesFirst(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "web/v2/state.ts", 9000, true, false),
		undocFile(root, "web/small/a.ts", 400, false, false),
		undocFile(root, "web/big/b.ts", 3000, false, false),
	}

	found := UndocumentedDirectories(root, files, nil)
	if got := undocDirs(found); len(got) != 2 || got[0] != "web/big" || got[1] != "web/small" {
		t.Fatalf("want the bigger directory first, got %v", got)
	}
}

// The project says for itself which kinds of file carry an anchor, by having
// anchored some. Without this the loudest thing the check reports can be the
// registry itself: a lockfile, locale JSON, a JSON schema, and the record
// YAML under `docs/facets/features/...` named as undocumented code — which is
// circular, since those files are the documentation. None of those types carries an anchor anywhere in the tree.
func TestAFileTypeThisProjectNeverAnchorsIsNotCounted(t *testing.T) {
	root := "/repo"
	files := []SourceFile{
		undocFile(root, "web/v2/state.ts", 4000, true, false),
		// A directory of data, in a type nothing in this tree anchors.
		undocFile(root, "web/locales/de.json", 3000, false, false),
		undocFile(root, "web/locales/en.json", 3000, false, false),
	}

	// The precondition, and the point: the same directory in a type this
	// project does anchor would be reported, so the case measures the file
	// type and not the size.
	asCode := []SourceFile{
		files[0],
		undocFile(root, "web/locales/de.ts", 3000, false, false),
		undocFile(root, "web/locales/en.ts", 3000, false, false),
	}
	if found := UndocumentedDirectories(root, asCode, nil); len(found) != 1 {
		t.Fatalf("fixture: the same directory must be reportable as code, got %v", undocDirs(found))
	}

	if found := UndocumentedDirectories(root, files, nil); len(found) != 0 {
		t.Fatalf("a type this project never anchors is not undocumented code, got %v", undocDirs(found))
	}
}
