// Fixtures below anchor a feature that exists only in a temporary directory,
// so this file opts itself out of anchor scanning: @facet-ignore-file

package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// driftFixture writes a repository, commits it, then applies the given
// working-tree edits. Drift is about what the working tree has moved, so
// there has to be a commit for it to have moved away from.
func driftFixture(t *testing.T, committed, edits map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range committed {
		write(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
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
	for name, body := range edits {
		write(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
	return root
}

// runDrift executes the real 'facet drift' command against root through the
// same cobra wiring the binary uses, and returns what it wrote to stdout — the
// surface an editor extension shells out to.
func runDrift(t *testing.T, args ...string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	root := newRootCmd()
	root.SetArgs(append([]string{"drift"}, args...))
	runErr := root.Execute()

	w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}

	if runErr != nil {
		t.Fatalf("facet drift: %v\n%s", runErr, out)
	}
	return string(out)
}

const driftFixtureRecord = "schema_version: \"0.1\"\nid: tasks/create/quick-add\nlifecycle: active\n"

// json-output-lists-drifted-anchors, and — because the process still has to
// exit cleanly for runDrift not to fail the test — nothing-here-fails-a-build.
func TestDriftJSONShapeAndRealDrift(t *testing.T) {
	root := driftFixture(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
		},
	)

	out := runDrift(t, "--json", "--root", root)

	var report facet.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(report.Drift) != 1 {
		t.Fatalf("want one drifted anchor, got %+v", report.Drift)
	}
	got := report.Drift[0]
	if got.ID != "tasks/create/quick-add" || got.Path != "src/quick_add.ts" {
		t.Errorf("drift lost its subject: %+v", got)
	}
	if got.ChangedAt == "" {
		t.Error("a change with no time is a change nobody can place")
	}
	if got.Registry != "" {
		t.Errorf("a single-registry project should not name one, got %q", got.Registry)
	}
	if len(report.StaleReferences) != 0 {
		t.Errorf("want no stale references, got %+v", report.StaleReferences)
	}
}

// human-readable-output-names-file-and-record
func TestDriftHumanReadableOutput(t *testing.T) {
	root := driftFixture(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
		},
	)

	out := runDrift(t, "--root", root)

	if !strings.Contains(out, "tasks/create/quick-add") || !strings.Contains(out, "src/quick_add.ts") {
		t.Errorf("human output should name the drifted feature and file, got %q", out)
	}
	if !strings.Contains(out, "1 drifted anchor") {
		t.Errorf("want a summary count, got %q", out)
	}
}

// a-record-that-moved-with-its-code-is-not-drift
func TestDriftIsSilentWhenTheRecordFollowed(t *testing.T) {
	root := driftFixture(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
			// The record moved with the code — that is the whole point.
			"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord + "intent:\n  summary: Now takes a line of input.\n",
		},
	)

	out := runDrift(t, "--json", "--root", root)

	var report facet.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(report.Drift) != 0 {
		t.Errorf("a record that moved with its code is not drift, got %+v", report.Drift)
	}
	out2 := runDrift(t, "--root", root)
	if !strings.Contains(out2, "No drift found") {
		t.Errorf("want the quiet message, got %q", out2)
	}
}

// json-output-lists-stale-references
func TestDriftReportsStaleReferencesFromRenames(t *testing.T) {
	root := driftFixture(t,
		map[string]string{
			"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord + `verification:
  unit_tests:
    - id: src/quick-add.test.ts
      proves: [adds]
`,
			"src/quick-add.test.ts": "test('adds', () => {});\n",
		},
		nil,
	)
	if err := os.MkdirAll(filepath.Join(root, "src/tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	mv := exec.Command("git", "-C", root, "mv", "src/quick-add.test.ts", "src/tasks/quick-add.test.ts")
	if out, err := mv.CombinedOutput(); err != nil {
		t.Skipf("git mv unavailable: %v (%s)", err, out)
	}

	out := runDrift(t, "--json", "--root", root)

	var report facet.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(report.StaleReferences) != 1 {
		t.Fatalf("want one stale reference, got %+v", report.StaleReferences)
	}
	s := report.StaleReferences[0]
	if s.ID != "tasks/create/quick-add" || s.Path != "src/quick-add.test.ts" || s.RenamedTo != "src/tasks/quick-add.test.ts" {
		t.Errorf("stale reference lost its subject: %+v", s)
	}

	textOut := runDrift(t, "--root", root)
	if !strings.Contains(textOut, "quick-add.test.ts") || !strings.Contains(textOut, "1 stale reference") {
		t.Errorf("human output should name the stale reference and count it, got %q", textOut)
	}
}

// the-check-runs-across-every-declared-registry: whether an anchor's record
// "followed" it is judged against every registry's record tree at once, not
// just the registry the anchor's own file belongs to — the id is global.
func TestDriftTouchedRecordsIsGlobalAcrossRegistries(t *testing.T) {
	root := driftFixture(t,
		map[string]string{
			".facetrc.yaml": `
registries:
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront]
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin]
`,
			"apps/storefront/docs/facets/features/auth/login/password.yaml": "schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n",
			"apps/admin/docs/facets/features/auth/login/password.yaml":      "schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n",
			"apps/storefront/login.go":                                      "// @facet: auth/login/password\npackage storefront\n",
		},
		map[string]string{
			// The storefront's anchored file changed...
			"apps/storefront/login.go": "// @facet: auth/login/password\npackage storefront\nfunc Login() {}\n",
			// ...and the *admin* registry's copy of the same global id followed
			// it, even though it anchors nothing in the storefront's own tree.
			"apps/admin/docs/facets/features/auth/login/password.yaml": "schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\nintent:\n  summary: SSO pending.\n",
		},
	)

	out := runDrift(t, "--json", "--root", root)

	var report facet.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(report.Drift) != 0 {
		t.Errorf("the id is global — a record that followed in another registry counts as followed, got %+v", report.Drift)
	}
}
