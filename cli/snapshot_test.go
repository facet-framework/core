package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runSnapshot runs 'facet snapshot' against a root and returns the envelope it
// wrote. The file is read back from disk rather than taken from a return value:
// the file is what a pipeline uploads, so the file is what is asserted on.
func runSnapshot(t *testing.T, root string, args ...string) snapshotEnvelope {
	t.Helper()
	out := filepath.Join(t.TempDir(), "snapshot.json")
	cmd := newRootCmd()
	var log strings.Builder
	cmd.SetOut(&log)
	cmd.SetErr(&log)
	cmd.SetArgs(append([]string{"snapshot", "--root", root, "--out", out}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("facet snapshot: %v\n%s", err, log.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("snapshot file was not written: %v", err)
	}
	var env snapshotEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("snapshot is not JSON: %v", err)
	}
	// Every snapshot in this file is checked for the building machine's path,
	// not one field of it: a path that leaks through drift, a product section or any field
	// added later is the same leak.
	if strings.Contains(string(raw), root) {
		t.Errorf("snapshot contains the local checkout path %q", root)
	}
	return env
}

func snapshotRepo(t *testing.T) string {
	t.Helper()
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "src/widget.go"),
		"package src\n\n// @"+"facet: demo/thing/widget\nfunc secretAlgorithm() int { return 42 }\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git is unavailable here: %v (%s)", err, out)
		}
	}
	return root
}

// The snapshot envelope contains the corpus and its source identity,
// including the commit, so anchor locations can be resolved against the
// revision that was measured.
func TestSnapshotCarriesTheCorpusAndItsOrigin(t *testing.T) {
	root := snapshotRepo(t)
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("git is unavailable here: %v", err)
	}

	env := runSnapshot(t, root)

	if env.Format != snapshotFormat {
		t.Errorf("format = %q, want %q", env.Format, snapshotFormat)
	}
	if got, want := env.Origin.Commit, strings.TrimSpace(string(head)); got != want {
		t.Errorf("commit = %q, want %q", got, want)
	}
	if env.Origin.GeneratedAt == "" {
		t.Error("generatedAt is empty: a snapshot must say when it was taken")
	}
	if _, ok := env.Corpus.Records["docs/facets/features/demo/thing/widget.yaml"]; !ok {
		t.Errorf("record demo/thing/widget missing from the corpus; have %d records", len(env.Corpus.Records))
	}
	if len(env.Corpus.Scan.Anchors["demo/thing/widget"]) != 1 {
		t.Errorf("anchors = %v, want one for demo/thing/widget", env.Corpus.Scan.Anchors)
	}
	if env.Corpus.Scan.Root != "" {
		t.Errorf("scan root = %q: a snapshot leaves the machine and must not carry a local path", env.Corpus.Scan.Root)
	}
}

// --no-excerpts removes each verbatim anchor source line. First assert that
// the default snapshot contains excerpts, then verify their removal.
func TestSnapshotCanLeaveTheSourceExcerptsBehind(t *testing.T) {
	root := snapshotRepo(t)

	with := runSnapshot(t, root)
	if a := with.Corpus.Scan.Anchors["demo/thing/widget"]; len(a) != 1 || !strings.Contains(a[0].Excerpt, "secretAlgorithm") {
		t.Fatalf("precondition: the default snapshot should carry the excerpt, got %+v", a)
	}

	without := runSnapshot(t, root, "--no-excerpts")
	for id, anchors := range without.Corpus.Scan.Anchors {
		for _, a := range anchors {
			if a.Excerpt != "" {
				t.Errorf("%s: excerpt %q survived --no-excerpts", id, a.Excerpt)
			}
		}
	}
	if !without.Origin.ExcerptsOmitted {
		t.Error("origin.excerptsOmitted = false: a consumer cannot tell an empty excerpt from a stripped one")
	}
}

// A snapshot leaves the machine; a token embedded in an https remote must not
// leave with it.
func TestSnapshotWritesTheRemoteWithoutCredentials(t *testing.T) {
	root := snapshotRepo(t)
	if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", "https://bot:s3cret@github.com/acme/widgets.git").CombinedOutput(); err != nil {
		t.Skipf("git is unavailable here: %v (%s)", err, out)
	}

	env := runSnapshot(t, root)

	if got, want := env.Origin.Remote, "https://github.com/acme/widgets.git"; got != want {
		t.Errorf("remote = %q, want %q", got, want)
	}
}

// A directory without records is refused by name, and no file is left that a
// pipeline could upload as an empty registry.
func TestSnapshotRefusesAProjectWithoutRecords(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "snapshot.json")
	cmd := newRootCmd()
	var log strings.Builder
	cmd.SetOut(&log)
	cmd.SetErr(&log)
	cmd.SetArgs([]string{"snapshot", "--root", root, "--out", out})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("snapshot of a directory without records succeeded")
	}
	if !strings.Contains(err.Error(), "docs/facets") {
		t.Errorf("error %q does not name where records were expected", err)
	}
	if !strings.Contains(err.Error(), "--root") || strings.Contains(err.Error(), "facet init") {
		t.Errorf("the error must give a supported adoption/root hint: %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("a file was written at %s", out)
	}
}
