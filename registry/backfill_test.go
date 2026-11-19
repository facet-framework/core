// @facet-ignore-file

package facet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- the synthetic repository ------------------------------------------------

// gitRepo is a throwaway repository with a handful of commits, which is the
// only honest way to test a tool whose whole job is reading git history. Held
// against this project's own log the walk would pass while its selection was
// wrong, because nothing there is known-good by construction.
type gitRepo struct {
	t    *testing.T
	root string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	r := &gitRepo{t: t, root: t.TempDir()}
	r.git("init", "--quiet", "--initial-branch=main")
	r.git("config", "user.email", "test@example.invalid")
	r.git("config", "user.name", "FACET Test")
	// Commit signing and hooks belong to whoever's machine this runs on, and
	// neither should reach a temporary repository.
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+r.root,
		"GIT_AUTHOR_NAME=FACET Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=FACET Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) write(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it on the given authored date, so
// --since has a stable history to cut against.
func (r *gitRepo) commit(subject, date string) string {
	r.t.Helper()
	r.git("add", "-A")
	cmd := exec.Command("git", "commit", "--quiet", "-m", subject)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+r.root,
		"GIT_AUTHOR_NAME=FACET Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=FACET Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_AUTHOR_DATE="+date+"T12:00:00+0000",
		"GIT_COMMITTER_DATE="+date+"T12:00:00+0000",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git commit: %v\n%s", err, out)
	}
	return r.git("rev-parse", "HEAD")
}

const thingRecord = `schema_version: "0.1"
id: app/core/thing
lifecycle: active
intent:
  summary: |
    Does the thing the application exists to do, which is enough of a summary
    for a record that only has to load.
contract:
  scenarios:
    - id: the-thing-happens
      statement: Given the application, when asked, then the thing happens
lineage:
  owner:
    team: app
  created_at: "2026-01-01"
`

// fixture builds a repository with one anchored feature and four commits: a
// feature, a fix that touches the anchored file, a chore, and a fix that
// touches only unanchored code.
type fixture struct {
	*gitRepo
	featSHA, fixSHA, choreSHA, orphanFixSHA string
	reg                                     Registry
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	r := newGitRepo(t)
	f := &fixture{gitRepo: r}

	r.write("docs/facets/features/app/core/thing.yaml", thingRecord)
	r.write("src/thing.go", "package src\n\n// @facet: app/core/thing\n// @facet-role: primary\nfunc Thing() {}\n")
	f.featSHA = r.commit("feat(app): the thing", "2026-01-05")

	r.write("src/thing.go", "package src\n\n// @facet: app/core/thing\n// @facet-role: primary\nfunc Thing() { fixed() }\n")
	f.fixSHA = r.commit("fix(app): the thing dropped its last row (#42)", "2026-02-10")

	r.write("src/unanchored.go", "package src\n\nfunc Other() {}\n")
	f.choreSHA = r.commit("chore: tidy up", "2026-03-01")

	r.write("src/unanchored.go", "package src\n\nfunc Other() { fixed() }\n")
	f.orphanFixSHA = r.commit("fix: something nothing anchors", "2026-04-01")

	f.reg = Open(r.root)
	return f
}

func (f *fixture) records(t *testing.T) []Record {
	t.Helper()
	recs, err := f.reg.LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func (f *fixture) anchors(t *testing.T) []Anchor {
	t.Helper()
	a, err := ScanAnchors(f.root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fixture) recordBody(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "docs/facets/features/app/core/thing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- selecting candidate commits --------------------------------------------

func TestBugfixCommitsSelectsConventionalFixesOnly(t *testing.T) {
	f := newFixture(t)
	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("want 2 fix commits, got %d: %+v", len(commits), commits)
	}
	got := map[string]Commit{}
	for _, c := range commits {
		got[c.SHA] = c
	}
	if _, ok := got[f.fixSHA]; !ok {
		t.Error("the anchored fix was not selected")
	}
	if _, ok := got[f.orphanFixSHA]; !ok {
		t.Error("the unanchored fix was not selected — selection is by message, not by resolution")
	}
	if _, ok := got[f.featSHA]; ok {
		t.Error("a feat: commit was selected")
	}
	if _, ok := got[f.choreSHA]; ok {
		t.Error("a chore: commit was selected")
	}

	fix := got[f.fixSHA]
	if fix.Date != "2026-02-10" {
		t.Errorf("date not read: %q", fix.Date)
	}
	if fix.Subject != "fix(app): the thing dropped its last row (#42)" {
		t.Errorf("subject not read: %q", fix.Subject)
	}
	if fix.Issue != "#42" {
		t.Errorf("issue reference not extracted: %q", fix.Issue)
	}
	if len(fix.Files) != 1 || fix.Files[0] != "src/thing.go" {
		t.Errorf("changed files not read: %v", fix.Files)
	}
}

func TestBugfixCommitsHonoursSinceAndLimit(t *testing.T) {
	f := newFixture(t)

	since, err := BugfixCommits(f.root, BackfillOptions{Since: "2026-03-15"})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].SHA != f.orphanFixSHA {
		t.Errorf("--since did not cut the history: %+v", since)
	}

	// The limit bounds the commits walked, not the fixes found, so a limit of
	// one over a history whose newest commit is a fix yields that one fix.
	limited, err := BugfixCommits(f.root, BackfillOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].SHA != f.orphanFixSHA {
		t.Errorf("--limit did not bound the walk: %+v", limited)
	}

	// Two commits back from HEAD is the chore and the orphan fix: one fix.
	limited2, err := BugfixCommits(f.root, BackfillOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited2) != 1 {
		t.Errorf("want 1 fix in the newest 2 commits, got %d", len(limited2))
	}
}

func TestBugfixCommitsAcceptsAnOverriddenPattern(t *testing.T) {
	f := newFixture(t)
	// Widened to any commit naming an issue — the documented secondary signal.
	commits, err := BugfixCommits(f.root, BackfillOptions{Match: `#[0-9]+`})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].SHA != f.fixSHA {
		t.Errorf("custom pattern not applied: %+v", commits)
	}
}

func TestBugfixCommitsRejectsAnUnparseablePattern(t *testing.T) {
	f := newFixture(t)
	if _, err := BugfixCommits(f.root, BackfillOptions{Match: "fix(["}); err == nil {
		t.Error("a malformed pattern must be an error, not a silent match-nothing")
	}
}

// --- attribution -------------------------------------------------------------

func TestBackfillRecordsOnlyTheAnchoredFix(t *testing.T) {
	f := newFixture(t)
	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if rep.Recorded != 1 {
		t.Errorf("want 1 fix recorded, got %d", rep.Recorded)
	}
	if len(rep.Unresolved) != 1 || rep.Unresolved[0].SHA != f.orphanFixSHA {
		t.Errorf("the unanchored fix must be reported unresolved, not dropped: %+v", rep.Unresolved)
	}

	rec := f.records(t)[0]
	fixes := rec.Fixes()
	if len(fixes) != 1 {
		t.Fatalf("want 1 fix on the record, got %d", len(fixes))
	}
	if fixes[0].Commit != f.fixSHA {
		t.Errorf("wrong commit recorded: %s", fixes[0].Commit)
	}
	if fixes[0].Date != "2026-02-10" {
		t.Errorf("wrong date recorded: %s", fixes[0].Date)
	}
	if len(fixes[0].Files) != 1 || fixes[0].Files[0] != "src/thing.go" {
		t.Errorf("only the anchored file belongs in files: %v", fixes[0].Files)
	}
	if fixes[0].Issue != "#42" {
		t.Errorf("issue not carried through: %q", fixes[0].Issue)
	}
	if fixes[0].RecordedBy != RecordedByGitHistory {
		t.Errorf("provenance not stamped: %q", fixes[0].RecordedBy)
	}

	// Everything the author wrote must survive a machine rewriting one block.
	body := f.recordBody(t)
	for _, want := range []string{"id: app/core/thing", "the-thing-happens", "created_at:", "lifecycle: active"} {
		if !strings.Contains(body, want) {
			t.Errorf("backfill destroyed %q:\n%s", want, body)
		}
	}
}

func TestBackfillOnlyCountsFilesAnchoredToTheFeature(t *testing.T) {
	f := newFixture(t)
	// A fix touching both an anchored and an unanchored file must record only
	// the anchored one: the file list is the evidence a reviewer checks, and a
	// path this feature does not own does not belong in it.
	f.write("src/thing.go", "package src\n\n// @facet: app/core/thing\n// @facet-role: primary\nfunc Thing() { again() }\n")
	f.write("src/unanchored.go", "package src\n\nfunc Other() { again() }\n")
	sha := f.commit("fix(app): both files at once", "2026-05-01")

	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, fix := range f.records(t)[0].Fixes() {
		if fix.Commit != sha {
			continue
		}
		if len(fix.Files) != 1 || fix.Files[0] != "src/thing.go" {
			t.Errorf("unanchored file leaked into the evidence: %v", fix.Files)
		}
		return
	}
	t.Error("the mixed commit was not recorded at all")
}

// --- idempotency -------------------------------------------------------------

func TestBackfillIsIdempotent(t *testing.T) {
	f := newFixture(t)
	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{}); err != nil {
		t.Fatal(err)
	}
	first := f.recordBody(t)

	rep, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Recorded != 0 {
		t.Errorf("a second run over the same range recorded %d more fixes", rep.Recorded)
	}
	if rep.Skipped != 1 {
		t.Errorf("want 1 already-known commit skipped, got %d", rep.Skipped)
	}
	if second := f.recordBody(t); second != first {
		t.Errorf("a second run rewrote the record:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestBackfillLeavesAnExcludedCommitAlone(t *testing.T) {
	f := newFixture(t)
	// A reviewer's correction has to survive the next run, or they would have
	// to make it again on every run — which is the same as not making it.
	body := thingRecord + `defects:
  excluded:
    - commit: "` + f.fixSHA + `"
      reason: the anchored file changed only to satisfy a linter
`
	f.write("docs/facets/features/app/core/thing.yaml", body)

	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Recorded != 0 {
		t.Errorf("an excluded commit was recorded anyway")
	}
	if n := f.records(t)[0].DefectTotal(); n != 0 {
		t.Errorf("want no fixes on the record, got %d", n)
	}
	if !strings.Contains(f.recordBody(t), "satisfy a linter") {
		t.Error("the exclusion and its reason must survive the rewrite")
	}
}

func TestBackfillDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	before := f.recordBody(t)
	commits, err := BugfixCommits(f.root, BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Backfill(f.reg, f.records(t), f.anchors(t), commits, BackfillOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Recorded != 1 {
		t.Errorf("a dry run must still report what it would do, got %d", rep.Recorded)
	}
	if len(rep.Changes) != 1 || rep.Changes[0].Feature != "app/core/thing" {
		t.Errorf("dry run did not describe the change: %+v", rep.Changes)
	}
	if f.recordBody(t) != before {
		t.Error("a dry run wrote to the record")
	}
}

// --- splicing the block into a hand-written file ------------------------------

func TestSpliceAppendsWhenTheBlockIsAbsent(t *testing.T) {
	src := "id: a/b/c\nlineage:\n  owner:\n    team: app\n"
	out := string(spliceTopLevelBlock([]byte(src), "defects", "defects:\n  fixes: []\n"))
	if !strings.HasPrefix(out, src) {
		t.Errorf("existing content was not preserved verbatim:\n%s", out)
	}
	if !strings.HasSuffix(out, "defects:\n  fixes: []\n") {
		t.Errorf("block not appended:\n%s", out)
	}
}

func TestSpliceReplacesTheBlockInPlaceAndKeepsWhatFollows(t *testing.T) {
	src := "id: a/b/c\n" +
		"defects:\n" +
		"  fixes:\n" +
		"    - commit: \"aaaaaaa\"\n" +
		"\n" +
		"lineage:\n" +
		"  owner:\n" +
		"    team: app\n"
	out := string(spliceTopLevelBlock([]byte(src), "defects", "defects:\n  fixes: []\n"))
	if strings.Contains(out, "aaaaaaa") {
		t.Errorf("the old block survived:\n%s", out)
	}
	if !strings.HasPrefix(out, "id: a/b/c\n") {
		t.Errorf("content before the block was disturbed:\n%s", out)
	}
	if !strings.Contains(out, "\nlineage:\n  owner:\n    team: app\n") {
		t.Errorf("content after the block was lost:\n%s", out)
	}
	// The blank line separating the block from what follows belongs to the
	// document, not to the block, so it must still be there.
	if !strings.Contains(out, "fixes: []\n\nlineage:") {
		t.Errorf("the separating blank line was swallowed:\n%s", out)
	}
}

func TestSpliceIgnoresAnIndentedKeyOfTheSameName(t *testing.T) {
	// 'defects' nested under another key is not the block being replaced.
	src := "id: a/b/c\nmetadata:\n  defects: counted by hand\n"
	out := string(spliceTopLevelBlock([]byte(src), "defects", "defects:\n  fixes: []\n"))
	if !strings.Contains(out, "  defects: counted by hand") {
		t.Errorf("a nested key of the same name was clobbered:\n%s", out)
	}
	if !strings.HasSuffix(out, "defects:\n  fixes: []\n") {
		t.Errorf("the top-level block was not appended:\n%s", out)
	}
}

func TestSpliceAddsTheMissingFinalNewline(t *testing.T) {
	out := string(spliceTopLevelBlock([]byte("id: a/b/c"), "defects", "defects:\n  fixes: []\n"))
	if !strings.Contains(out, "id: a/b/c\ndefects:") {
		t.Errorf("a file without a trailing newline was joined onto the block:\n%q", out)
	}
}

// --- rendering ----------------------------------------------------------------

func TestRenderedBlockQuotesWhatYAMLWouldOtherwiseRetype(t *testing.T) {
	block := renderDefectsBlock(
		[]Fix{{
			Commit:  "1234567890123456789012345678901234567890",
			Date:    "2026-02-10",
			Files:   []string{"src/thing.go"},
			Subject: `fix: "quoted", colon: and \backslash`,
			Issue:   "#42", RecordedBy: RecordedByGitHistory,
		}},
		nil,
	)
	// An all-digit hash and a bare date both parse as non-strings in YAML, and
	// the round trip through the YAML parser is what would break.
	if !strings.Contains(block, `commit: "1234567890123456789012345678901234567890"`) {
		t.Errorf("an all-digit hash was left unquoted:\n%s", block)
	}
	if !strings.Contains(block, `date: "2026-02-10"`) {
		t.Errorf("a date was left unquoted, so YAML will hand it back as a timestamp:\n%s", block)
	}
	if !strings.Contains(block, `\"quoted\"`) || !strings.Contains(block, `\\backslash`) {
		t.Errorf("subject not escaped:\n%s", block)
	}

	// And it must survive the parser it was written for.
	rec := writeRecord(t, "schema_version: \"0.1\"\nid: a/b/c\n"+block)
	got := rec.Fixes()
	if len(got) != 1 {
		t.Fatalf("the rendered block did not parse back: %s", block)
	}
	if got[0].Commit != "1234567890123456789012345678901234567890" {
		t.Errorf("commit did not round-trip: %q", got[0].Commit)
	}
	if got[0].Date != "2026-02-10" {
		t.Errorf("date did not round-trip: %q", got[0].Date)
	}
	if got[0].Subject != `fix: "quoted", colon: and \backslash` {
		t.Errorf("subject did not round-trip: %q", got[0].Subject)
	}
}

func TestRenderedBlockIsOmittedWhenThereIsNothingToSay(t *testing.T) {
	if block := renderDefectsBlock(nil, nil); block != "" {
		t.Errorf("an empty ledger must render nothing, got:\n%s", block)
	}
}
