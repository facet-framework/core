// The fixtures below anchor features that exist only in a temporary directory,
// so this file opts itself out of anchor scanning: @facet-ignore-file

package facet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitProject writes a repository, commits it, and then applies the given
// working-tree edits. Drift is about what the working tree has moved, so there
// has to be a commit for it to have moved away from.
func gitProject(t *testing.T, committed, edits map[string]string) (string, Registry) {
	t.Helper()
	root := t.TempDir()

	write := func(files map[string]string) {
		for name, body := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	write(committed)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "-m", "first version"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is unavailable here: %v (%s)", err, out)
		}
	}
	write(edits)

	return root, Open(root)
}

const driftRecord = `schema_version: "0.1"
id: tasks/create/quick-add
lifecycle: active
`

func TestChangedFilesSeesTheWorkingTree(t *testing.T) {
	root, _ := gitProject(t,
		map[string]string{"src/a.ts": "export const a = 1;\n"},
		map[string]string{"src/a.ts": "export const a = 2;\n", "src/b.ts": "export const b = 1;\n"},
	)

	changed := ChangedFiles(root)

	want := map[string]bool{"src/a.ts": true, "src/b.ts": true}
	if len(changed) != len(want) {
		t.Fatalf("want %v, got %v", want, changed)
	}
	for _, rel := range changed {
		if !want[rel] {
			t.Errorf("unexpected path %q", rel)
		}
	}
}

// GitChangeSet enumerates individual files in new directories with
// --untracked-files=all so drift checks and hooks can scan their anchors.
func TestChangedFilesNamesFilesInWhollyNewDirectories(t *testing.T) {
	root, _ := gitProject(t,
		map[string]string{"src/a.ts": "export const a = 1;\n"},
		map[string]string{"src/newmodule/b.ts": "export const b = 1;\n"},
	)

	changed := ChangedFiles(root)

	found := false
	for _, rel := range changed {
		if rel == "src/newmodule/b.ts" {
			found = true
		}
	}
	if !found {
		t.Errorf("want src/newmodule/b.ts named, got %v", changed)
	}
}

func TestStaleReferencesNamesRecordsPointingAtTheOldPath(t *testing.T) {
	root, reg := gitProject(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord + `verification:
  unit_tests:
    - id: src/quick-add.test.ts
      proves: [adds]
`,
			"src/quick-add.test.ts": "test('adds', () => {});\n",
		},
		nil,
	)
	mv := exec.Command("git", "-C", root, "mv", "src/quick-add.test.ts", "src/tasks/quick-add.test.ts")
	if err := os.MkdirAll(filepath.Join(root, "src/tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := mv.CombinedOutput(); err != nil {
		t.Skipf("git mv unavailable: %v (%s)", err, out)
	}

	renames := RenamedFiles(root)
	if renames["src/quick-add.test.ts"] != "src/tasks/quick-add.test.ts" {
		t.Fatalf("RenamedFiles = %v, want the move recorded", renames)
	}

	stale := StaleReferences(reg, renames)
	if got := stale["tasks/create/quick-add"]; len(got) != 1 || got[0] != "src/quick-add.test.ts" {
		t.Errorf("StaleReferences = %v, want the record to name the old path", stale)
	}
}

// Without a rename there is nothing to say, and nothing to read.
func TestStaleReferencesIsEmptyWithoutRenames(t *testing.T) {
	_, reg := gitProject(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": driftRecord,
	}, nil)
	if got := StaleReferences(reg, nil); len(got) != 0 {
		t.Errorf("want nothing, got %v", got)
	}
}

func TestChangedFilesIsEmptyWithoutGit(t *testing.T) {

	if got := ChangedFiles(t.TempDir()); len(got) != 0 {
		t.Errorf("want nothing outside a repository, got %v", got)
	}
}

func TestDriftedAnchorsReportsSourceWhoseRecordStoodStill(t *testing.T) {
	root, reg := gitProject(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
		},
	)

	anchors := []Anchor{{ID: "tasks/create/quick-add", Path: filepath.Join(root, "src/quick_add.ts"), Line: 1}}
	changed := ChangedFiles(root)
	known := map[string]bool{"tasks/create/quick-add": true}

	drift := DriftedAnchors(reg, anchors, changed, TouchedRecords([]Registry{reg}, changed), known)

	if len(drift) != 1 {
		t.Fatalf("want one drifted anchor, got %v", drift)
	}
	if drift[0].ID != "tasks/create/quick-add" || drift[0].Path != "src/quick_add.ts" {
		t.Errorf("drift lost its subject: %+v", drift[0])
	}
	if drift[0].ChangedAt == "" {
		t.Error("a change with no time is a change nobody can place")
	}
}

func TestDriftedAnchorsIsSilentWhenTheRecordFollowed(t *testing.T) {
	root, reg := gitProject(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
			// The record moved with the code — that is the whole point.
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord + "intent:\n  summary: Now takes a line of input.\n",
		},
	)

	anchors := []Anchor{{ID: "tasks/create/quick-add", Path: filepath.Join(root, "src/quick_add.ts"), Line: 1}}
	changed := ChangedFiles(root)

	drift := DriftedAnchors(reg, anchors, changed, TouchedRecords([]Registry{reg}, changed),
		map[string]bool{"tasks/create/quick-add": true})

	if len(drift) != 0 {
		t.Errorf("a record that moved with its code is not drift, got %v", drift)
	}
}

func TestDriftedAnchorsIgnoresAnchorsInFilesThatDidNotChange(t *testing.T) {
	root, reg := gitProject(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
			"src/other.ts":     "export const other = 1;\n",
		},
		map[string]string{"src/other.ts": "export const other = 2;\n"},
	)

	anchors := []Anchor{{ID: "tasks/create/quick-add", Path: filepath.Join(root, "src/quick_add.ts"), Line: 1}}
	changed := ChangedFiles(root)

	drift := DriftedAnchors(reg, anchors, changed, TouchedRecords([]Registry{reg}, changed),
		map[string]bool{"tasks/create/quick-add": true})

	if len(drift) != 0 {
		t.Errorf("an untouched anchored file is not drift, got %v", drift)
	}
}

func TestDriftedAnchorsSkipsIdsNoRecordAnswersFor(t *testing.T) {
	// An anchor with no record is the linter's finding, not a drifted anchor.
	root, reg := gitProject(t,
		map[string]string{"src/edit.ts": "// @facet: tasks/edit/inline\nexport function edit() {}\n"},
		map[string]string{"src/edit.ts": "// @facet: tasks/edit/inline\nexport function edit(x: string) {}\n"},
	)

	anchors := []Anchor{{ID: "tasks/edit/inline", Path: filepath.Join(root, "src/edit.ts"), Line: 1}}
	changed := ChangedFiles(root)

	drift := DriftedAnchors(reg, anchors, changed, TouchedRecords([]Registry{reg}, changed), map[string]bool{})

	if len(drift) != 0 {
		t.Errorf("an orphaned anchor is not drift, got %v", drift)
	}
}

func TestDriftedAnchorsReportsEachFileOncePerRecord(t *testing.T) {
	root, reg := gitProject(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nfunction a() {}\n// @facet: tasks/create/quick-add\nfunction b() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nfunction a(x: string) {}\n// @facet: tasks/create/quick-add\nfunction b() {}\n",
		},
	)

	path := filepath.Join(root, "src/quick_add.ts")
	anchors := []Anchor{
		{ID: "tasks/create/quick-add", Path: path, Line: 1},
		{ID: "tasks/create/quick-add", Path: path, Line: 3},
	}
	changed := ChangedFiles(root)

	drift := DriftedAnchors(reg, anchors, changed, TouchedRecords([]Registry{reg}, changed),
		map[string]bool{"tasks/create/quick-add": true})

	if len(drift) != 1 {
		t.Errorf("two anchors in one changed file are one drifted file, got %v", drift)
	}
}

// Deleted source content is read from git so drift checks can recover its
// anchors even though the working-tree file no longer exists.
func TestDeletedFilesNamesRemovedPaths(t *testing.T) {
	root, _ := gitProject(t,
		map[string]string{"src/a.ts": "// @facet: tasks/create/quick-add\n", "src/b.ts": "export const b = 1;\n"},
		nil,
	)
	if err := os.Remove(filepath.Join(root, "src", "a.ts")); err != nil {
		t.Fatal(err)
	}

	deleted := DeletedFiles(root)

	if len(deleted) != 1 || deleted[0] != "src/a.ts" {
		t.Fatalf("want [src/a.ts], got %v", deleted)
	}
}

// A rename moves a path; it does not remove the feature. RenamedFiles reports
// those, and reporting them here as well would say a feature lost its code when
// the code only moved.
func TestDeletedFilesIgnoresRenames(t *testing.T) {
	root, _ := gitProject(t, map[string]string{"src/a.ts": "export const a = 1;\n"}, nil)
	mv := exec.Command("git", "-C", root, "mv", "src/a.ts", "src/b.ts")
	if out, err := mv.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v (%s)", err, out)
	}

	if deleted := DeletedFiles(root); len(deleted) != 0 {
		t.Errorf("want nothing, got %v", deleted)
	}
}

// Mutation: use newline porcelain and trim quoting rather than NUL-delimited
// status. Unicode, quoted, arrow-containing and newline paths are then lost.
func TestChangedFilesPreservesExactGitPaths(t *testing.T) {
	want := map[string]string{"src/naïve-café.ts": "export const x = 1;\n", "src/a -> b.ts": "export const x = 1;\n", "src/quote\".ts": "export const x = 1;\n", "src/line\nbreak.ts": "export const x = 1;\n", "src/trailing .ts ": "export const x = 1;\n"}
	root, _ := gitProject(t, map[string]string{"src/old.ts": "export const old = 1;\n"}, want)
	got := ChangedFiles(root)
	if len(got) != len(want) {
		t.Fatalf("changed paths: %q, want %d", got, len(want))
	}
	for _, path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("path corrupted: %q", path)
		}
	}
}

// Mutation: do not consume the second NUL field of a rename. The original
// path then appears as a changed path instead of only the destination.
func TestChangedFilesUsesRenameDestination(t *testing.T) {
	root, _ := gitProject(t, map[string]string{"src/old -> name.ts": "export const x = 1;\n"}, nil)
	git(t, root, "mv", "src/old -> name.ts", "src/naïve-café.ts")
	// Check Git really classified this as a rename before testing the parser.
	if got := git(t, root, "status", "--porcelain"); len(got) < 2 || got[0] != 'R' {
		t.Fatalf("rename premise: %q", got)
	}
	paths := ChangedFiles(root)
	if len(paths) != 1 || paths[0] != "src/naïve-café.ts" {
		t.Fatalf("rename destination: %q", paths)
	}
}
