// The fixtures below anchor features that exist only in a temporary directory,
// so this file opts itself out of anchor scanning: @facet-ignore-file

package facet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	quickAddID     = "tasks/create/quick-add"
	quickAddRecord = "docs/facets/features/tasks/create/quick-add.yaml"
)

func gitIn(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func knownQuickAdd() map[string]bool { return map[string]bool{quickAddID: true} }

// referenceChangesOf loads a change set the way every consumer does and asks it
// about the default registry.
func referenceChangesOf(t *testing.T, root string, reg Registry) []ReferenceChange {
	t.Helper()
	cs := LoadChangeSet(root, "")
	if !cs.OK() {
		t.Fatalf("the fixture's baseline did not compare: %+v", cs.Baseline)
	}
	current, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return cs.ReferenceChanges(reg, current, knownQuickAdd())
}

// Mutation: in parseNameStatusZ, take the destination for a rename from
// fields[i] instead of fields[i+1] — every rename then reports its old path as
// its new one. And: replace the -z output with porcelain split on " -> " —
// the arrow and the quote cases turn red.
func TestPathsSurviveSpacesQuotesNonASCIIAndArrows(t *testing.T) {
	odd := []string{
		"src/with space.ts",
		`src/with "quote".ts`,
		"src/umlaut-ä.ts",
		"src/a -> b.ts",
	}
	committed := map[string]string{}
	for _, p := range odd {
		committed[p] = "export const x = 1;\n"
	}
	root, _ := gitProject(t, committed, nil)
	// Assert that git quotes the fixture names in textual output, so the
	// parser is exercised against quoted paths.
	if status := gitIn(t, root, "-c", "core.quotePath=true", "ls-files"); !strings.Contains(status, `"src/`) {
		t.Fatalf("the fixture does not exercise quoting: %q", status)
	}

	gitIn(t, root, "mv", "src/a -> b.ts", "src/moved -> here ä.ts")
	if err := os.Remove(filepath.Join(root, "src/with space.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, `src/with "quote".ts`), []byte("export const x = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/new ö -> x.ts"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Untracked and quoted on both ends, so trimming quotes off an untracked
	// path is caught as well as splitting it.
	if err := os.WriteFile(filepath.Join(root, `src/"fresh".ts`), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := LoadChangeSet(root, "")
	if !cs.OK() {
		t.Fatalf("baseline: %+v", cs.Baseline)
	}
	if got := cs.Renamed()["src/a -> b.ts"]; got != "src/moved -> here ä.ts" {
		t.Errorf("rename lost its paths: %v", cs.Renamed())
	}
	if d := cs.Deleted(); len(d) != 1 || d[0] != "src/with space.ts" {
		t.Errorf("deletion lost its path: %v", d)
	}
	changed := strings.Join(cs.Changed(), "\n")
	for _, want := range []string{`src/with "quote".ts`, "src/new ö -> x.ts", "src/moved -> here ä.ts", `src/"fresh".ts`} {
		found := false
		for _, p := range cs.Changed() {
			found = found || p == want
		}
		if !found {
			t.Errorf("changed paths miss %q exactly:\n%s", want, changed)
		}
	}
}

// Mutation: drop the 'D' case from ReferenceChanges' status filter — both a
// staged and an unstaged deletion then report nothing.
func TestADeletedAnchoredFileIsALostReferenceStagedOrNot(t *testing.T) {
	for _, staged := range []bool{false, true} {
		root, reg := gitProject(t, map[string]string{
			quickAddRecord:     driftRecord,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\n// @facet-role: primary\nexport function quickAdd() {}\n",
		}, nil)
		if staged {
			gitIn(t, root, "rm", "-q", "src/quick_add.ts")
		} else if err := os.Remove(filepath.Join(root, "src/quick_add.ts")); err != nil {
			t.Fatal(err)
		}

		refs := referenceChangesOf(t, root, reg)

		if len(refs) != 1 {
			t.Fatalf("staged=%v: want one lost reference, got %+v", staged, refs)
		}
		r := refs[0]
		if r.Kind != RefDeleted || r.ID != quickAddID || r.Path != "src/quick_add.ts" ||
			r.Role != "primary" || r.Evidence != EvidenceBaseline || r.Surviving != 0 || !r.Lost() {
			t.Errorf("staged=%v: the reference lost its subject: %+v", staged, r)
		}
	}
}

// The case orphan-records cannot see: another implementation survives.
func TestARemovedAnchorIsReportedWhileAnotherSurvives(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\n// @facet-role: primary\nexport function quickAdd() {}\n",
		"src/helper.ts":    "// @facet: tasks/create/quick-add\n// @facet-role: related\nexport function helper() {}\n",
	}, map[string]string{
		"src/helper.ts": "export function helper() {}\n",
	})

	refs := referenceChangesOf(t, root, reg)

	if len(refs) != 1 {
		t.Fatalf("want one lost reference, got %+v", refs)
	}
	r := refs[0]
	if r.Kind != RefAnchorRemoved || r.Path != "src/helper.ts" || r.Role != "related" || r.Surviving != 1 {
		t.Errorf("want the related anchor removed from helper.ts with one surviving, got %+v", r)
	}
}

// Mutation: report every renamed file's anchors as RefAnchorRemoved (drop the
// 'standing[a.ID] && R' case) — a rename that carried its anchor then reads as
// a loss.
// Provenance mutation: restore before = after on an unchanged rename. The
// evidence assertion then catches the filesystem path replacing its source.
func TestARenameThatCarriesItsAnchorIsNotALoss(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
	}, nil)
	if err := os.MkdirAll(filepath.Join(root, "src/tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "mv", "src/quick_add.ts", "src/tasks/quick add.ts")

	refs := referenceChangesOf(t, root, reg)

	if len(refs) != 1 || refs[0].Kind != RefRenamed || refs[0].Lost() ||
		refs[0].Path != "src/quick_add.ts" || refs[0].NewPath != "src/tasks/quick add.ts" ||
		refs[0].Evidence != EvidenceBaseline {
		t.Fatalf("want one rename carrying the anchor, got %+v", refs)
	}
}

// Mutation: skip the status check after ReferenceChanges. A failed pickaxe
// query then becomes an empty result rather than a missing-evidence warning.
func TestLintReportsAReferenceQueryThatFailsAfterBaselineResolution(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
	}, map[string]string{
		"src/quick_add.ts": "export function quickAdd() {}\n",
	})
	cs := LoadChangeSet(root, "")
	if !cs.OK() {
		t.Fatalf("precondition: baseline resolution failed: %+v", cs)
	}
	current, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	wrapper := "#!/bin/sh\nfor arg do\ncase \"$arg\" in -G*) echo simulated-pickaxe-failure >&2; exit 77;; esac\ndone\nexec '" + strings.ReplaceAll(git, "'", "'\\''") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var rep Report
	lintLostAnchors(reg, &rep, &cs, current, knownQuickAdd())
	if cs.Baseline.Status != BaselineUnavailable {
		t.Fatalf("precondition: pickaxe query did not fail: %+v", cs.Baseline)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Check != "lost-anchors" ||
		rep.Findings[0].Severity != SeverityWarning ||
		!strings.Contains(rep.Findings[0].Message, "simulated-pickaxe-failure") {
		t.Fatalf("want missing-evidence warning, got %+v", rep.Findings)
	}
}

// Mutation: in ReferenceChanges, read a renamed file's before-state from the
// working tree instead of anchorsBefore — the dropped anchor is then never
// seen.
func TestARenameThatDropsItsAnchorIsALoss(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\nexport const a = 1;\nexport const b = 2;\nexport const c = 3;\n",
	}, nil)
	gitIn(t, root, "mv", "src/quick_add.ts", "src/add.ts")
	if err := os.WriteFile(filepath.Join(root, "src/add.ts"),
		[]byte("export function quickAdd() {}\nexport const a = 1;\nexport const b = 2;\nexport const c = 3;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "-A")

	refs := referenceChangesOf(t, root, reg)

	if len(refs) != 1 || refs[0].Kind != RefAnchorRemoved || refs[0].NewPath != "src/add.ts" {
		t.Fatalf("want the anchor reported as removed in the renamed file, got %+v", refs)
	}
}

// Restoring the file is the whole remedy, and the event has to go with it.
func TestRestoringTheFileClearsTheEvent(t *testing.T) {
	root, reg := gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
	}, nil)
	if err := os.Remove(filepath.Join(root, "src/quick_add.ts")); err != nil {
		t.Fatal(err)
	}
	if refs := referenceChangesOf(t, root, reg); len(refs) != 1 {
		t.Fatalf("precondition: the deletion must be seen first, got %+v", refs)
	}

	gitIn(t, root, "checkout", "--", "src/quick_add.ts")

	if refs := referenceChangesOf(t, root, reg); len(refs) != 0 {
		t.Errorf("a restored file lost nothing, got %+v", refs)
	}
}

// Mutation: make LoadChangeSet return Status BaselineOK with no files when the
// revision does not resolve — the missing-revision and no-commit cases then
// read as "no drift".
func TestAMissingBaselineIsNotNoDrift(t *testing.T) {
	t.Run("no git at all", func(t *testing.T) {
		cs := LoadChangeSet(t.TempDir(), "")
		if cs.OK() || cs.Baseline.Status != BaselineNoRepository {
			t.Errorf("want no-repository, got %+v", cs.Baseline)
		}
	})
	t.Run("a revision that does not exist", func(t *testing.T) {
		root, _ := gitProject(t, map[string]string{"src/a.ts": "x\n"}, nil)
		cs := LoadChangeSet(root, "no-such-revision")
		if cs.OK() || cs.Baseline.Status != BaselineMissing || cs.Baseline.Kind != BaselineExplicit {
			t.Errorf("want a missing explicit baseline, got %+v", cs.Baseline)
		}
	})
	t.Run("a repository with no commit", func(t *testing.T) {
		root := t.TempDir()
		gitIn(t, root, "init", "-q")
		if err := os.WriteFile(filepath.Join(root, "a.ts"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cs := LoadChangeSet(root, "")
		if cs.OK() || cs.Baseline.Status != BaselineMissing {
			t.Errorf("want a missing baseline, got %+v", cs.Baseline)
		}
		// The new files are still visible to callers that look for them.
		if got := cs.Changed(); len(got) != 1 || got[0] != "a.ts" {
			t.Errorf("want the uncommitted file listed, got %v", got)
		}
	})
	t.Run("a clean tree against HEAD", func(t *testing.T) {
		root, _ := gitProject(t, map[string]string{"src/a.ts": "x\n"}, nil)
		cs := LoadChangeSet(root, "")
		if !cs.OK() || len(cs.Files) != 0 || cs.Baseline.Kind != BaselineHead || cs.Baseline.Reviewed() {
			t.Errorf("want a clean, unreviewed HEAD comparison, got %+v", cs.Baseline)
		}
	})
}

// Mutation: drop the per-registry '!reg.Claims(oldPath)' filter — the
// storefront's deletion is then reported by the admin registry as well.
func TestEachRegistryReportsOnlyTheFilesItClaims(t *testing.T) {
	root, _ := gitProject(t, map[string]string{
		".facetrc.yaml": "registries:\n  storefront:\n    records: apps/storefront/docs/facets\n    sources: [apps/storefront]\n  admin:\n    records: apps/admin/docs/facets\n    sources: [apps/admin]\n",
		"apps/storefront/docs/facets/features/auth/login/password.yaml": "schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n",
		"apps/admin/docs/facets/features/auth/login/password.yaml":      "schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n",
		"apps/storefront/login.go":                                      "// @facet: auth/login/password\npackage storefront\n",
		"apps/admin/login.go":                                           "// @facet: auth/login/password\npackage admin\n",
	}, nil)
	if err := os.Remove(filepath.Join(root, "apps/storefront/login.go")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ComputeDrift(root, cfg.All(), "")
	if err != nil {
		t.Fatal(err)
	}

	if len(report.ReferenceChanges) != 1 {
		t.Fatalf("want one lost reference, got %+v", report.ReferenceChanges)
	}
	r := report.ReferenceChanges[0]
	if r.Registry != "storefront" || r.Path != "apps/storefront/login.go" || r.Surviving != 0 {
		t.Errorf("want the storefront's own loss, got %+v", r)
	}
}

// acceptFixture creates an explicit review baseline local to this worktree.
func acceptFixture(t *testing.T) (string, Registry) {
	t.Helper()
	return gitProject(t, map[string]string{
		quickAddRecord:     driftRecord,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		"src/other.ts":     "// @facet: tasks/create/quick-add\nexport function other() {}\n",
	}, nil)
}

func driftIDs(t *testing.T, root string, reg Registry) []string {
	t.Helper()
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range report.Drift {
		out = append(out, d.Path)
	}
	return out
}

func writeRel(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Mutation: make AcceptReview a no-op that returns nil — the accepted change
// is still reported.
func TestAReviewedChangeCanBeAcceptedWithoutTouchingTheRecord(t *testing.T) {
	root, reg := acceptFixture(t)
	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string) {}\n")
	if got := driftIDs(t, root, reg); len(got) != 1 {
		t.Fatalf("precondition: the change must be drift first, got %v", got)
	}
	recordBefore, _ := os.ReadFile(filepath.Join(root, quickAddRecord))
	statusBefore := gitIn(t, root, "status", "--porcelain")

	b, err := AcceptReview(root, nil, "behavior-unchanged")
	if err != nil {
		t.Fatal(err)
	}

	if got := driftIDs(t, root, reg); len(got) != 0 {
		t.Errorf("an accepted change is not drift any more, got %v", got)
	}
	if b.Kind != BaselineAccepted || b.Outcome != "behavior-unchanged" || b.AcceptedAt == "" {
		t.Errorf("the baseline must say it is an accepted review: %+v", b)
	}
	recordAfter, _ := os.ReadFile(filepath.Join(root, quickAddRecord))
	if string(recordAfter) != string(recordBefore) {
		t.Error("accepting must not rewrite the record")
	}
	if got := gitIn(t, root, "status", "--porcelain"); got != statusBefore {
		t.Errorf("accepting must not touch the index or the tree:\nbefore %q\nafter  %q", statusBefore, got)
	}
}

// The acceptance is the reviewed content, not the path: the snapshot holds the
// file's blob, and a later edit differs from it. Mutation: compare by path
// only, by making the accepted tree from every working-tree path but diffing
// with '--name-only --diff-filter=ADR' — the edit is then invisible.
func TestALaterChangeInvalidatesTheAcceptance(t *testing.T) {
	root, reg := acceptFixture(t)
	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string) {}\n")
	if _, err := AcceptReview(root, nil, ""); err != nil {
		t.Fatal(err)
	}

	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string, y: number) {}\n")

	if got := driftIDs(t, root, reg); len(got) != 1 || got[0] != "src/quick_add.ts" {
		t.Errorf("a change after the acceptance must be reported again, got %v", got)
	}
}

// Mutation: in FollowedRecords, drop the Reviewed() branch — an edited record
// then dismisses source drift again even after a review was recorded.
func TestAnUnrelatedRecordEditDoesNotDismissDriftOnceReviewsAreRecorded(t *testing.T) {
	root, reg := acceptFixture(t)
	if _, err := AcceptReview(root, nil, ""); err != nil {
		t.Fatal(err)
	}
	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string) {}\n")
	// A Knowledge wording fix — nothing about the changed source.
	writeRel(t, root, quickAddRecord, driftRecord+"knowledge:\n  not_for:\n    question: \"When not?\"\n    answer: Not for subtasks.\n")

	if got := driftIDs(t, root, reg); len(got) != 1 {
		t.Errorf("a record edit is not a review of the source, got %v", got)
	}
}

// Mutation: make LoadChangeSet always compare against HEAD — the commit made
// after the accepted baseline then disappears from the report.
func TestChangesCommittedAfterTheBaselineStayReported(t *testing.T) {
	root, reg := acceptFixture(t)
	if _, err := AcceptReview(root, nil, ""); err != nil {
		t.Fatal(err)
	}
	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string) {}\n")
	gitIn(t, root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qam", "change the code")

	if got := gitIn(t, root, "status", "--porcelain"); got != "" {
		t.Fatalf("precondition: the tree must be clean, got %q", got)
	}
	if got := driftIDs(t, root, reg); len(got) != 1 {
		t.Errorf("a clean tree is not a reviewed one — the committed change must stay reported, got %v", got)
	}
}

// Mutation: in AcceptReview, ignore the paths argument (always add ".") — the
// unnamed file is then accepted as well.
func TestAcceptingOnePathLeavesTheOthersUnreviewed(t *testing.T) {
	root, reg := acceptFixture(t)
	writeRel(t, root, "src/quick_add.ts", "// @facet: tasks/create/quick-add\nexport function quickAdd(x: string) {}\n")
	writeRel(t, root, "src/other.ts", "// @facet: tasks/create/quick-add\nexport function other(x: string) {}\n")

	if _, err := AcceptReview(root, []string{"src/quick_add.ts"}, "references-repaired"); err != nil {
		t.Fatal(err)
	}

	if got := driftIDs(t, root, reg); len(got) != 1 || got[0] != "src/other.ts" {
		t.Errorf("only the named path is reviewed, got %v", got)
	}
}

// Mutation: store the baseline under refs/facet/drift-baseline instead of
// refs/worktree/ — the second worktree then sees the first one's review, and
// a 'git push --mirror' would carry it.
func TestTheReviewStaysInItsWorktreeAndOutOfEveryBranch(t *testing.T) {
	root, _ := acceptFixture(t)
	other := filepath.Join(t.TempDir(), "second")
	gitIn(t, root, "worktree", "add", "-q", other)

	if _, err := AcceptReview(root, nil, ""); err != nil {
		t.Fatal(err)
	}

	if cs := LoadChangeSet(other, ""); cs.Baseline.Kind != BaselineHead {
		t.Errorf("another worktree must not inherit this one's review, got %+v", cs.Baseline)
	}
	shared := gitIn(t, root, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags", "refs/remotes")
	if strings.Contains(shared, "facet") {
		t.Errorf("the review must not be a branch, tag or remote ref: %q", shared)
	}
	// Nothing a branch reaches contains the review: HEAD's history is the
	// project's, and the snapshot is not part of it.
	if got := gitIn(t, root, "log", "--format=%s", "HEAD"); strings.Contains(got, "facet drift accept") {
		t.Errorf("the review commit must not be reachable from HEAD: %q", got)
	}
	if err := ClearReview(root); err != nil {
		t.Fatal(err)
	}
	if cs := LoadChangeSet(root, ""); cs.Baseline.Kind != BaselineHead {
		t.Errorf("reset must fall back to HEAD, got %+v", cs.Baseline)
	}
}

func TestAnUnknownOutcomeIsRefused(t *testing.T) {
	root, _ := acceptFixture(t)
	if _, err := AcceptReview(root, nil, "looks-fine"); err == nil {
		t.Error("an outcome outside the documented set must be refused")
	}
}

func TestAcceptingUntrackedFilesDoesNotStageThemOrKeepReportingThem(t *testing.T) {
	root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord, "src/old.ts": "export const old = 1;\n"}, nil)
	path := filepath.Join(root, "src/new.ts")
	body := "// @facet: tasks/create/quick-add\nexport const fresh = 1;\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	index := gitIn(t, root, "ls-files", "--stage")
	if _, err := AcceptReview(root, []string{"src/new.ts"}, "record-updated"); err != nil {
		t.Fatal(err)
	}
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil || len(report.Drift) != 0 || len(report.ReferenceChanges) != 0 || !LoadChangeSet(root, "").OK() {
		t.Fatalf("accepted untracked file still reported: %+v %v", report, err)
	}
	if gitIn(t, root, "ls-files", "--stage") != index || !strings.Contains(gitIn(t, root, "status", "--porcelain"), "?? src/new.ts") {
		t.Fatal("acceptance staged the file")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(body, "fresh = 1", "fresh = 2", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	report, err = ComputeDrift(root, []Registry{reg}, "")
	if err != nil || len(report.Drift) != 1 || report.Drift[0].Path != "src/new.ts" || len(report.ReferenceChanges) != 0 {
		t.Fatalf("later untracked edit missed or called a deletion: %+v %v", report, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	report, err = ComputeDrift(root, []Registry{reg}, "")
	if err != nil || len(report.ReferenceChanges) != 1 || report.ReferenceChanges[0].Kind != RefDeleted || report.ReferenceChanges[0].Path != "src/new.ts" {
		t.Fatalf("later untracked deletion missed: %+v %v", report, err)
	}
}
