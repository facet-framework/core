// Fixtures here contain example @facet: tags; this file demonstrates the
// history the check reads rather than implementing a feature, so it opts out
// of scanning.
// @facet-ignore-file

package facet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// commitOnDay writes a file and commits it on a given day, so a fixture can
// place a record and its code apart in history.
func commitOnDay(t *testing.T, root, rel, body, day string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := day + "T12:00:00+00:00"
	for _, args := range [][]string{{"add", rel}, {"commit", "-q", "-m", "add " + rel}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func newDatedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return root
}

func datedRecordYAML(id, created string) string {
	return fmt.Sprintf(`schema_version: "0.1"
id: %s
lifecycle: active
intent:
  summary: |
    A fixture.
contract:
  scenarios:
    - id: something-holds
      statement: |
        Given a, when b, then c.
lineage:
  owner:
    team: t
  created_at: "%s"
`, id, created)
}

// createdAtFindings returns the created-at findings for one record laid out in a real
// repository: the record file added on recordDay, its anchored source on
// codeDay.
func createdAtFindings(t *testing.T, created, recordDay, codeDay string) []Finding {
	t.Helper()
	root := newDatedRepo(t)
	rel := "docs/facets/features/a/b/thing.yaml"
	commitOnDay(t, root, "src/thing.go", "// @facet: a/b/thing\npackage x\n", codeDay)
	commitOnDay(t, root, rel, datedRecordYAML("a/b/thing", created), recordDay)

	reg := Registry{Root: root, RecordsDir: filepath.Join(root, "docs/facets/features")}
	rec := Record{Path: filepath.Join(root, rel), ID: "a/b/thing", Lifecycle: "active",
		Fields: map[string]any{"lineage": map[string]any{"created_at": created}}}
	anchors := map[string][]Anchor{"a/b/thing": {{ID: "a/b/thing", Path: "src/thing.go", Line: 1}}}

	var rep Report
	lintCreatedAt(reg, &rep, []Record{rec}, anchors, FirstAdded(root))
	return rep.Findings
}

func createdAtMessages(found []Finding) string {
	var parts []string
	for _, f := range found {
		parts = append(parts, f.Message)
	}
	return strings.Join(parts, " | ")
}

// Direction A. Named mutation: replace `day.After(arrived)` with
// `arrived.After(day)` in lintCreatedAt — it builds and vets, and this case
// goes red while the two below stay green.
func TestACreatedAtLaterThanItsOwnFileIsReported(t *testing.T) {
	found := createdAtFindings(t, "2026-09-20", "2026-09-01", "2026-09-01")

	if len(found) != 1 {
		t.Fatalf("want 1 finding, got %d: %s", len(found), createdAtMessages(found))
	}
	if !strings.Contains(found[0].Message, "newer than the file") {
		t.Fatalf("the finding does not say what is wrong: %s", found[0].Message)
	}
	if found[0].Check != "created-at-history" || found[0].Severity != SeverityWarning {
		t.Fatalf("wrong check or severity: %q %v", found[0].Check, found[0].Severity)
	}
}

func TestACreatedAtFarBeforeTheCodeIsReported(t *testing.T) {
	// A 16-day gap between the date and the code.
	found := createdAtFindings(t, "2026-08-25", "2026-08-25", "2026-09-10")

	if len(found) != 1 {
		t.Fatalf("want 1 finding, got %d: %s", len(found), createdAtMessages(found))
	}
	if !strings.Contains(found[0].Message, "16 days before the oldest code") {
		t.Fatalf("the finding does not name the measured gap: %s", found[0].Message)
	}
}

func TestACreatedAtShortlyBeforeTheCodeIsNotReported(t *testing.T) {
	found := createdAtFindings(t, "2026-09-06", "2026-09-06", "2026-09-10")

	if len(found) != 0 {
		t.Fatalf("want silence inside the corridor, got: %s", createdAtMessages(found))
	}
}

// The boundary itself, so the threshold is pinned rather than approximately
// right. 14 days is silent, 15 speaks.
func TestTheThresholdIsWhereItWasMeasuredToBe(t *testing.T) {
	if found := createdAtFindings(t, "2026-08-27", "2026-08-27", "2026-09-10"); len(found) != 0 {
		t.Fatalf("14 days must stay silent, got: %s", createdAtMessages(found))
	}
	if found := createdAtFindings(t, "2026-08-26", "2026-08-26", "2026-09-10"); len(found) != 1 {
		t.Fatalf("15 days must be reported, got %d", len(found))
	}
}

// Without history the check says nothing — and "nothing" is not "in order".
func TestWithoutHistoryNothingIsClaimed(t *testing.T) {
	root := t.TempDir() // no git init
	rel := "docs/facets/features/a/b/thing.yaml"
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(datedRecordYAML("a/b/thing", "1999-01-01")), 0o644); err != nil {
		t.Fatal(err)
	}

	added := FirstAdded(root)
	if len(added) != 0 {
		t.Fatalf("a directory that is not a repository must yield no history, got %d entries", len(added))
	}

	reg := Registry{Root: root, RecordsDir: filepath.Join(root, "docs/facets/features")}
	rec := Record{Path: path, ID: "a/b/thing", Lifecycle: "active",
		Fields: map[string]any{"lineage": map[string]any{"created_at": "1999-01-01"}}}

	var rep Report
	lintCreatedAt(reg, &rep, []Record{rec}, map[string][]Anchor{}, added)

	// 1999 is absurd against any real history. The check still says nothing,
	// because it has not looked.
	if len(rep.Findings) != 0 {
		t.Fatalf("want silence without history, got: %s", createdAtMessages(rep.Findings))
	}
}

// The oldest anchor determines the creation date independently of scan
// order; a later-added anchor must not replace the earliest anchor day.
func TestTheOldestAnchorDecidesAndNotTheFirstFound(t *testing.T) {
	root := newDatedRepo(t)
	rel := "docs/facets/features/a/b/thing.yaml"
	commitOnDay(t, root, "src/original.go", "// @facet: a/b/thing\npackage x\n", "2026-07-26")
	commitOnDay(t, root, rel, datedRecordYAML("a/b/thing", "2026-07-26"), "2026-07-26")
	// A second anchor, added seven weeks later — the shape that produces a
	// false positive when the newest anchor decides.
	commitOnDay(t, root, "src/later_test.go", "// @facet: a/b/thing\npackage x\n", "2026-09-15")

	reg := Registry{Root: root, RecordsDir: filepath.Join(root, "docs/facets/features")}
	rec := Record{Path: filepath.Join(root, rel), ID: "a/b/thing", Lifecycle: "active",
		Fields: map[string]any{"lineage": map[string]any{"created_at": "2026-07-26"}}}
	// The later anchor first, an order a scan may return.
	anchors := map[string][]Anchor{"a/b/thing": {
		{ID: "a/b/thing", Path: "src/later_test.go", Line: 1},
		{ID: "a/b/thing", Path: "src/original.go", Line: 1},
	}}

	var rep Report
	lintCreatedAt(reg, &rep, []Record{rec}, anchors, FirstAdded(root))

	if len(rep.Findings) != 0 {
		t.Fatalf("a later anchor must not make the record look early, got: %s", createdAtMessages(rep.Findings))
	}
}

// A path that was added, deleted and added again keeps its first day: the
// later add is a return, not an origin. Without this the check would call a
// record early whenever its code had been moved out and back.
func TestAReaddedPathKeepsItsFirstDay(t *testing.T) {
	root := newDatedRepo(t)
	commitOnDay(t, root, "src/thing.go", "package x\n", "2026-05-01")

	cmd := exec.Command("git", "rm", "-q", "src/thing.go")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git rm: %v\n%s", err, out)
	}
	commit := exec.Command("git", "commit", "-q", "-m", "remove")
	commit.Dir = root
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-06-01T12:00:00+00:00", "GIT_COMMITTER_DATE=2026-06-01T12:00:00+00:00",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	commitOnDay(t, root, "src/thing.go", "package x // back\n", "2026-07-01")

	if day := FirstAdded(root)["src/thing.go"]; day != "2026-05-01" {
		t.Fatalf("want the first day 2026-05-01, got %q", day)
	}
}

// A shallow clone is a truncated history, and it is what CI hands the check by
// default: actions/checkout fetches depth 1 unless a workflow says otherwise.
// Every file then looks as though it arrived in the single commit that exists.
func TestAShallowCloneIsTreatedAsNoHistory(t *testing.T) {
	origin := newDatedRepo(t)
	commitOnDay(t, origin, "src/thing.go", "package x\n", "2026-05-01")
	commitOnDay(t, origin, "src/other.go", "package x\n", "2026-09-01")

	shallow := filepath.Join(t.TempDir(), "shallow")
	clone := exec.Command("git", "clone", "--depth", "1", "--quiet", "file://"+origin, shallow)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("git clone --depth 1: %v\n%s", err, out)
	}

	// The full history knows both days; the shallow one must claim neither.
	if day := FirstAdded(origin)["src/thing.go"]; day != "2026-05-01" {
		t.Fatalf("the full clone should know the first day, got %q", day)
	}
	if added := FirstAdded(shallow); len(added) != 0 {
		t.Fatalf("a shallow clone must yield no history, got %d entries: %v", len(added), added)
	}
}
