// Fixtures below contain example @facet: tags; this file exercises the hook
// handlers rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/registry"
)

// adoptedRepo builds a git repository holding one registry with one record.
func adoptedRepo(t *testing.T, id string) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is unavailable here: %v (%s)", err, out)
		}
	}
	write(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
		"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\n")
	return root
}

// TestRootFollowsTheFileBeingEdited resolves the checkout from the edited
// file, including when the host session started in another worktree.
// Environment-only lookup would inspect the wrong registry and drift state.
func TestRootFollowsTheFileBeingEdited(t *testing.T) {
	launched := adoptedRepo(t, "demo/thing/widget")
	worktree := adoptedRepo(t, "demo/thing/newcomer")
	edited := filepath.Join(worktree, "src/x.ts")
	write(t, edited, "// @facet: demo/thing/newcomer\nexport const x = 1;\n")

	t.Setenv("CLAUDE_PROJECT_DIR", launched)
	var in hookInput
	in.CWD = launched
	in.ToolInput.FilePath = edited

	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if got := ctx.root; got != worktree {
		t.Errorf("root = %q, want the worktree %q", got, worktree)
	}
}

// A path that belongs to no registry of its own must not narrow the project:
// a submodule or a nested repository documents nothing, and treating it as the
// root would make the hooks go quiet on a project that has records.
func TestRootFallsBackWhenTheFileHasNoRegistry(t *testing.T) {
	launched := adoptedRepo(t, "demo/thing/widget")
	plain := t.TempDir()
	edited := filepath.Join(plain, "src/x.ts")
	write(t, edited, "export const x = 1;\n")

	t.Setenv("CLAUDE_PROJECT_DIR", launched)
	var in hookInput
	in.CWD = launched
	in.ToolInput.FilePath = edited

	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if got := ctx.root; got != launched {
		t.Errorf("root = %q, want the launch directory %q", got, launched)
	}
}

// The reminder is suppressed after delivery and repeats after the record
// changes, so a later change in the same session receives a fresh notice.
func TestNoticeSpeaksAgainAfterTheRecordMoves(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	key := anchorNotice(facet.Open("/fixture"), "demo/thing/widget", "src/main.ts")

	if !noticeIsNew("s1", key) {
		t.Fatal("want the first mention to be new")
	}
	if noticeIsNew("s1", key) {
		t.Error("want silence on the second edit of the same file")
	}

	clearNotices("s1", anchorNotice(facet.Open("/fixture"), "demo/thing/widget", ""))

	if !noticeIsNew("s1", key) {
		t.Error("want the reminder to speak again once the record has moved")
	}
}

// Clearing one feature's markers must not silence, or revive, another's.
func TestClearNoticesLeavesOtherFeaturesAlone(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	other := anchorNotice(facet.Open("/fixture"), "demo/thing/other", "src/other.ts")
	noticeIsNew("s1", anchorNotice(facet.Open("/fixture"), "demo/thing/widget", "src/main.ts"))
	noticeIsNew("s1", other)

	clearNotices("s1", anchorNotice(facet.Open("/fixture"), "demo/thing/widget", ""))

	if noticeIsNew("s1", other) {
		t.Error("want the other feature's marker to survive")
	}
}

// Markers are per session. Expiration removes old sessions while retaining
// active session notices to bound the plugin data directory.
func TestExpireNoticesRemovesOldSessionsOnly(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_DATA", data)
	noticeIsNew("old-session", "anchor:a/b/c:src/x.ts")
	noticeIsNew("fresh-session", "anchor:a/b/c:src/y.ts")

	old := filepath.Join(data, "notices", "old-session")
	past := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	expireNotices(30 * 24 * time.Hour)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("want the old session's markers gone")
	}
	if _, err := os.Stat(filepath.Join(data, "notices", "fresh-session")); err != nil {
		t.Error("want this session's markers kept")
	}
}

func TestAnyPrimary(t *testing.T) {
	related := []facet.Anchor{{ID: "a/b/c", Role: "related"}}
	if anyPrimary(related) {
		t.Error("want related-only to be recognised as such")
	}
	if !anyPrimary([]facet.Anchor{{ID: "a/b/c", Role: "related"}, {ID: "d/e/f", Role: "primary"}}) {
		t.Error("want a primary anchor to carry the file")
	}
	// Anchors written before '@facet-role:' existed meant primary.
	if !anyPrimary([]facet.Anchor{{ID: "a/b/c"}}) {
		t.Error("want an anchor without a role to count as primary")
	}
}

// condense is what keeps the digest a reminder rather than a second copy of the
// record: one line, cut at a word boundary.
func TestCondense(t *testing.T) {
	if got := condense("  one\n  two   three ", 40); got != "one two three" {
		t.Errorf("condense = %q, want the prose on one line", got)
	}
	long := condense("the quick brown fox jumps over the lazy dog", 20)
	if len(long) > 24 || !strings.HasSuffix(long, "...") {
		t.Errorf("condense = %q, want a short line ending in an ellipsis", long)
	}
	if strings.Contains(long, "jump") {
		t.Errorf("condense = %q, want the cut at a word boundary", long)
	}
}

// Outside a FACET project the hooks stay inert rather than inventing a registry.
func TestNoContextOutsideAFacetProject(t *testing.T) {
	plain := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", plain)
	t.Chdir(plain)
	var in hookInput
	in.CWD = plain
	if _, ok := resolveHookContext(newHookCmd(), in); ok {
		t.Error("want no context outside a FACET project")
	}
}

// TestLooksLikeCommit covers command forms including git -C /path commit,
// where the path is an option argument rather than the subcommand.
func TestLooksLikeCommit(t *testing.T) {
	commits := []string{
		"git commit -m x",
		"git commit --amend --no-edit",
		"cd /tmp && git commit -m x",
		"git -C /home/user/worktrees/feature commit -m x",
		"git -C /path commit",
		"git -c user.name=bot commit -m x",
		"git --git-dir /r/.git --work-tree /r commit -m x",
		"git --git-dir=/r/.git commit -m x",
		"git -C /path -c core.hooksPath=/dev/null commit -m x",
		"git --no-pager commit -m x",
		"GIT_AUTHOR_NAME=bot git commit -m x",
		"git add -A && git commit -m x",
	}
	for _, cmd := range commits {
		if !looksLikeCommit(cmd) {
			t.Errorf("looksLikeCommit(%q) = false, want true", cmd)
		}
	}

	others := []string{
		"git status",
		"git -C /path status",
		"git log --oneline",
		"git add -A",
		"echo git commit",
		"git",
		"git -C /path",
		"npm run commit",
		"git commitizen",
	}
	for _, cmd := range others {
		if looksLikeCommit(cmd) {
			t.Errorf("looksLikeCommit(%q) = true, want false", cmd)
		}
	}
}

// captureHook runs a handler and returns the context it emitted.
func captureHook(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	// Drain the pipe while the hook runs: output beyond pipe capacity would
	// otherwise block the writer before assertions could inspect the result.
	read := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(r)
		read <- out
	}()
	run()
	w.Close()
	os.Stdout = old
	return string(<-read)
}

// The roster is handed over at the two moments an agent has none: once because
// it never had one, once because compaction summarised the one it had away.
// Both are asserted, because wiring one and forgetting the other is exactly the
// failure this closes — and it would look like it worked.
func TestTheRecordIndexIsGivenAtSessionStartAndAgainBeforeCompaction(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices, newest first.\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-01-01\"\n")
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\nexport const list = 1;\n")
	ctx := newHookContext(root)

	for name, run := range map[string]func(){
		"SessionStart": func() { hookSessionStart(ctx) },
		"PreCompact":   func() { hookPreCompact(ctx, "session-1") },
	} {
		out := captureHook(t, run)
		if !strings.Contains(out, "FACET record index") {
			t.Errorf("%s carries no index:\n%s", name, out)
		}
		if !strings.Contains(out, "billing/invoice/list [active] (billing)") {
			t.Errorf("%s does not name the record:\n%s", name, out)
		}
		if !strings.Contains(out, "Lists a customer's invoices, newest first.") {
			t.Errorf("%s carries no summary, so nothing can be routed by it:\n%s", name, out)
		}
	}
}

// PreCompact obtains changed records and anchored sources from one git
// status snapshot, avoiding a duplicate status traversal on large worktrees.
func TestPreCompactTakesOneWorkingTreeSnapshot(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	ctx := newHookContext(root)

	calls := 0
	changedFiles := func(gotRoot string) []string {
		calls++
		if gotRoot != root {
			t.Fatalf("snapshot root = %q, want %q", gotRoot, root)
		}
		return []string{"docs/facets/features/billing/invoice/list.yaml"}
	}

	preCompactChanges([]hookContext{ctx}, changedFiles)
	if calls != 1 {
		t.Fatalf("working-tree snapshots = %d, want 1", calls)
	}
}

func TestLargeRegistryIndexDoesNotParseEveryRecord(t *testing.T) {
	root := adoptedRepo(t, "demo/feature/000")
	for i := 1; i <= 400; i++ {
		path := filepath.Join(root, "docs/facets/features/demo/feature", fmt.Sprintf("%03d.yaml", i))
		write(t, path, "not: [valid yaml")
	}

	index := largeRegistryIndex(newHookContext(root))
	if !strings.Contains(index, "all 401 record(s)") {
		t.Fatalf("large index has the wrong count:\n%s", index)
	}
	if !strings.Contains(index, "- demo/feature/400") {
		t.Fatalf("large index omitted the last id:\n%s", index)
	}
}

// The fast index has to say where the record files are.
func TestTheLargeRegistryIndexSaysWhereTheRecordsAre(t *testing.T) {
	root := adoptedRepo(t, "demo/feature/000")
	for i := 1; i <= 400; i++ {
		path := filepath.Join(root, "docs/facets/features/demo/feature", fmt.Sprintf("%03d.yaml", i))
		write(t, path, "not: [valid yaml")
	}

	index := largeRegistryIndex(newHookContext(root))

	// The precondition: this really is the fast path, so a pass below is about
	// the header this path prints and not about the rich index's.
	if !strings.Contains(index, "the lines carry ids only") {
		t.Fatalf("this is not the fast path:\n%s", index)
	}
	for _, sentence := range []string{
		"docs/facets/features/<id>.yaml",
		"relative to the repository root",
		"Never prefix one with '/'",
	} {
		if !strings.Contains(index, sentence) {
			t.Errorf("the fast index never says %q:\n%s", sentence, index)
		}
	}
}

// sizedRecord writes one active record with the given scenario count, anchored
// in source so it is not simultaneously an orphan.
func sizedRecord(t *testing.T, root, id string, scenarios int) string {
	t.Helper()
	var body strings.Builder
	for i := 0; i < scenarios; i++ {
		body.WriteString("    - id: scenario-" + string(rune('a'+i)) + "\n")
		body.WriteString("      statement: |\n        Given a customer\n        When they act\n        Then something observable happens\n")
	}
	path := filepath.Join(root, "docs/facets/features", id+".yaml")
	write(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n"+body.String()+"lineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: "+id+"\n// @facet-role: primary\nexport const list = 1;\n")
	return path
}

// Saving a record reports sizing feedback immediately, before later records
// can repeat the same granularity error.
func TestSizingIsRaisedWhenTheRecordIsWritten(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	path := sizedRecord(t, root, "billing/invoice/list", 1)

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "sizing-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}

	out := captureHook(t, func() { hookPostToolUse(ctx, in) })
	if !strings.Contains(out, "single contract scenario") {
		t.Fatalf("want the sizing warning on the save, got %q", out)
	}
	if !strings.Contains(out, "question, not a defect") {
		t.Fatalf("want the warning framed as a question, got %q", out)
	}

	// Sizing feedback appears once per session per record, avoiding duplicate
	// notices on successive saves.
	again := captureHook(t, func() { hookPostToolUse(ctx, in) })
	if strings.Contains(again, "single contract scenario") {
		t.Fatalf("want the sizing warning only once per session, got it twice: %q", again)
	}
}

func TestSizingIsSilentOnAWellSizedRecord(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	path := sizedRecord(t, root, "billing/invoice/list", 4)

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "well-sized-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if out := captureHook(t, func() { hookPostToolUse(ctx, in) }); strings.Contains(out, "sizing") {
		t.Fatalf("want silence on a well-sized record, got %q", out)
	}
}

// An undeclared id segment is an error, and errors already reach the author
// through this hook. The test pins that the taxonomy check is among them —
// the id is immutable, so the save is the last cheap moment to catch it.
func TestUndeclaredTaxonomySegmentIsRaisedWhenTheRecordIsWritten(t *testing.T) {
	root := adoptedRepo(t, "ledger/invoice/list")
	path := sizedRecord(t, root, "ledger/invoice/list", 4)
	write(t, filepath.Join(root, "docs/facets/taxonomy.yaml"),
		"schema_version: \"0.1\"\ndomains:\n  billing:\n    title: Billing\n    capabilities:\n      invoice:\n        title: Invoices\n")

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "taxonomy-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}

	out := captureHook(t, func() { hookPostToolUse(ctx, in) })
	if !strings.Contains(out, "taxonomy does not declare") {
		t.Fatalf("want the undeclared domain reported on the save, got %q", out)
	}
	// The hint separates a typo from a missing node.
	if !strings.Contains(out, "billing") {
		t.Fatalf("want the declared domains in the hint, got %q", out)
	}
}

// A project that declares no taxonomy must hear nothing about one, however its
// ids are shaped. This is the backward-compatibility guarantee, at the surface
// where a regression would be loudest.
func TestTaxonomySilentWithoutADeclaration(t *testing.T) {
	root := adoptedRepo(t, "whatever/nonsense/thing")
	path := sizedRecord(t, root, "whatever/nonsense/thing", 4)

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "no-taxonomy-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if out := captureHook(t, func() { hookPostToolUse(ctx, in) }); strings.Contains(out, "taxonomy") {
		t.Fatalf("want silence without a declared taxonomy, got %q", out)
	}
}

// brokenRegistry builds a repository whose lint fails with an error: an anchor
// naming a record that does not exist.
func brokenRegistry(t *testing.T) string {
	t.Helper()
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	// The defect: an anchor for a record nobody wrote.
	write(t, filepath.Join(root, "src/ghost.ts"), "// @facet: billing/invoice/ghost\n// @facet-role: primary\nexport const ghost = 1;\n")
	return root
}

func preToolUseFor(t *testing.T, root, command string) (hookContext, hookInput) {
	t.Helper()
	var in hookInput
	in.CWD = root
	in.SessionID = "gate-session"
	in.ToolInput.Command = command
	if _, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD").Output(); err != nil {
		git(t, root, "add", ".")
	}
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	return ctx, in
}

// A recognised commit command must reach the gate and be denied when the
// registry violates its checks. Exercise the hook output, not recognition alone.
func TestCommitGateRefusesAFailingRegistry(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	ctx, in := preToolUseFor(t, root, "git commit -m wip")

	out := captureHook(t, func() { hookPreToolUse(ctx, in) })

	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("want the commit denied, got %q", out)
	}
	// The denial names both the failing id and the documented bypass, so the
	// caller can identify the failure and explicitly override the gate.
	if !strings.Contains(out, "billing/invoice/ghost") {
		t.Fatalf("want the failing id in the reason, got %q", out)
	}
	if !strings.Contains(out, "FACET_SKIP_COMMIT_GATE=1") {
		t.Fatalf("want the escape hatch named in the reason, got %q", out)
	}
}

// The commit gate supports the documented host-environment bypass so a
// blocked commit can proceed under explicit session-owner control.
func TestCommitGateHasAnEscapeHatch(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "1")
	root := brokenRegistry(t)
	ctx, in := preToolUseFor(t, root, "git commit -m wip")

	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want silence when the hatch is set, got %q", out)
	}
}

func TestCommitGateLetsACleanRegistryThrough(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	ctx, in := preToolUseFor(t, root, "git commit -m wip")

	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want a clean registry to commit silently, got %q", out)
	}
}

// Warnings are work for an owner, not a broken build — the gate must not read
// them as a reason to refuse, or every empty Knowledge slot blocks a commit.
func TestCommitGateIgnoresWarnings(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := adoptedRepo(t, "billing/invoice/list")
	// One scenario: a sizing warning, and no Knowledge block at all.
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	ctx, in := preToolUseFor(t, root, "git commit -m wip")

	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want warnings not to block a commit, got %q", out)
	}
}

// Anything that is not a commit passes untouched. The gate runs on every Bash
// call, so a false positive here costs a lint run on every command.
func TestCommitGateIgnoresNonCommits(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	for _, cmd := range []string{"git status", "echo git commit", "ls"} {
		ctx, in := preToolUseFor(t, root, cmd)
		if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
			t.Fatalf("%q should not be gated, got %q", cmd, out)
		}
	}
}

// anchorProject builds a repository with one real record, and returns a helper
// that runs the anchor check over a source file written on the spot.
func anchorProject(t *testing.T, session string) (func(name, body string) string, string) {
	t.Helper()
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices, newest first, so they can reconcile billing.\ncontract:\n  scenarios:\n    - id: newest-first\n      statement: |\n        Given invoices\n        Then they are listed newest first\n    - id: empty-state-shown\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")

	run := func(name, body string) string {
		path := filepath.Join(root, name)
		write(t, path, body)
		var in hookInput
		in.CWD = root
		in.SessionID = session
		in.ToolInput.FilePath = path
		ctx, ok := resolveHookContext(newHookCmd(), in)
		if !ok {
			t.Fatal("want a context, got none")
		}
		return captureHook(t, func() { hookCheckAnchors(ctx, in, path) })
	}
	return run, root
}

// Exercise the anchor reminder through hookCheckAnchors and assert the
// message delivered for an edited anchored source.
func TestAnchorReminderReportsADanglingAnchor(t *testing.T) {
	run, _ := anchorProject(t, "dangling-session")

	out := run("src/ghost.ts", "// @facet: billing/invoice/ghost\n// @facet-role: primary\nexport const g = 1;\n")

	if !strings.Contains(out, "do not resolve") {
		t.Fatalf("want the dangling anchor reported, got %q", out)
	}
	if !strings.Contains(out, "billing/invoice/ghost") {
		t.Fatalf("want the id named, got %q", out)
	}
	// The remedy has to name where the record would live, or the reader has to
	// derive the path from the id themselves.
	if !strings.Contains(out, "features/billing/invoice/ghost.yaml") {
		t.Fatalf("want the expected record path, got %q", out)
	}
}

// Anchor reminders distinguish related sources from primary implementation,
// where the contract is implemented.
func TestAnchorReminderTellsRelatedApartFromImplementation(t *testing.T) {
	run, _ := anchorProject(t, "roles-session")

	primary := run("src/list.ts", "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	if !strings.Contains(primary, "implements FACET feature(s)") {
		t.Fatalf("want the implementation wording, got %q", primary)
	}

	related := run("src/helper.ts", "// @facet: billing/invoice/list\n// @facet-role: related\nexport const h = 1;\n")
	if !strings.Contains(related, "is related to FACET feature(s)") {
		t.Fatalf("want the related wording, got %q", related)
	}
	if strings.Contains(related, "update the matching record") {
		t.Fatalf("a related file must not get the full obligation, got %q", related)
	}
}

// The reminder is only worth reading if it says what the record claims. Without
// this, recordDigest could return nothing and the reminder would still look fine.
func TestAnchorReminderCarriesWhatTheRecordClaims(t *testing.T) {
	run, _ := anchorProject(t, "digest-session")

	out := run("src/list.ts", "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")

	if !strings.Contains(out, "newest first") {
		t.Fatalf("want the intent in the reminder, got %q", out)
	}
	for _, id := range []string{"newest-first", "empty-state-shown"} {
		if !strings.Contains(out, id) {
			t.Fatalf("want scenario %q in the reminder, got %q", id, out)
		}
	}
	if !strings.Contains(out, "12.1") {
		t.Fatalf("want the obligation cited, got %q", out)
	}
}

// A feature reminder is emitted once per feature and file, avoiding duplicate
// prompts on successive edits.
func TestAnchorReminderIsDeduplicatedPerFile(t *testing.T) {
	run, _ := anchorProject(t, "dedup-session")
	body := "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n"

	if first := run("src/list.ts", body); !strings.Contains(first, "implements FACET") {
		t.Fatalf("want the first edit to speak, got %q", first)
	}
	if again := run("src/list.ts", body+"// touched\n"); again != "" {
		t.Fatalf("want silence on the second edit of the same file, got %q", again)
	}
	// A different file carrying the same feature is a new obligation.
	if other := run("src/list-extra.ts", body); !strings.Contains(other, "implements FACET") {
		t.Fatalf("want a second file to speak, got %q", other)
	}
}

func TestAnchorReminderSilentWithoutAnchors(t *testing.T) {
	run, _ := anchorProject(t, "quiet-session")
	if out := run("src/plain.ts", "export const nothing = 1;\n"); out != "" {
		t.Fatalf("want silence on a file with no anchor, got %q", out)
	}
}

func realSchema(t *testing.T, recordsDir string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "schema", "facet.schema.json"))
	if err != nil {
		t.Skipf("schema not available: %v", err)
	}
	write(t, filepath.Join(recordsDir, "schema", "facet.schema.json"), string(src))
}

// validRecord is a record that passes the shipped schema, parameterised only by
// the id it declares — so a test that changes the id changes nothing else.
func validRecord(id string) string {
	return "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n"
}

// The validation hook checks ID/path agreement: an anchored record ID is
// immutable, so a mismatched path must be reported when the record is saved.
func TestWriteValidationReportsAnIdThatContradictsItsPath(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	realSchema(t, filepath.Join(root, "docs/facets"))
	// The file lives at billing/invoice/list.yaml and says it is something else.
	path := filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	write(t, path, validRecord("billing/invoice/somethingelse"))
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "id-path-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}

	out := captureHook(t, func() { hookPostToolUse(ctx, in) })

	// Both halves, because either alone leaves the author guessing which is
	// wrong — the file's name or the record's own id.
	if !strings.Contains(out, "billing/invoice/somethingelse") {
		t.Fatalf("want the declared id in the report, got %q", out)
	}
	if !strings.Contains(out, "billing/invoice/list") {
		t.Fatalf("want the expected id in the report, got %q", out)
	}
	if !strings.Contains(out, "need fixing now") {
		t.Fatalf("want it raised as an error at write time, got %q", out)
	}
}

// The counterpart: a record whose id agrees with its path hears nothing about
// one, or the check is noise on every save.
func TestWriteValidationIsSilentWhenTheIdMatchesItsPath(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	realSchema(t, filepath.Join(root, "docs/facets"))
	path := filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	write(t, path, validRecord("billing/invoice/list"))
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")

	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "id-path-clean-session"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}

	if out := captureHook(t, func() { hookPostToolUse(ctx, in) }); strings.Contains(out, "path implies") {
		t.Fatalf("want silence when the id agrees with its path, got %q", out)
	}
}

func TestWriteValidationDoesNotLoadSiblingRecords(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	realSchema(t, filepath.Join(root, "docs/facets"))
	path := filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	write(t, path, validRecord("billing/invoice/somethingelse"))
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/broken.yaml"), "[: invalid")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	in := hookInput{ToolName: "Write", SessionID: "single-record", CWD: root}
	in.ToolInput.FilePath = path
	out := captureHook(t, func() { hookPostToolUse(newHookContext(root), in) })
	if !strings.Contains(out, "billing/invoice/somethingelse") {
		t.Fatalf("saved record was not validated independently: %q", out)
	}
}

// twoRegistriesWithDifferentSchemas builds a monorepo whose two registries
// vendor genuinely different schemas: 'admin' additionally requires a
// 'metadata' block, 'storefront' does not.
func twoRegistriesWithDifferentSchemas(t *testing.T) (root, record string) {
	t.Helper()
	root = t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), `
registries:
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin]
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront]
`)
	src, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "schema", "facet.schema.json"))
	if err != nil {
		t.Skipf("schema not available: %v", err)
	}
	write(t, filepath.Join(root, "apps/storefront/docs/facets/schema/facet.schema.json"), string(src))

	var doc map[string]any
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	required, ok := doc["required"].([]any)
	if !ok {
		t.Fatalf("the shipped schema has no top-level 'required' list; this fixture assumed one: %v", doc["required"])
	}
	doc["required"] = append(required, "metadata")
	stricter, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "apps/admin/docs/facets/schema/facet.schema.json"), string(stricter))

	// Both registries must be adopted, or the hook context drops one and the
	// test would pass for the wrong reason.
	write(t, filepath.Join(root, "apps/admin/docs/facets/features/billing/invoice/other.yaml"),
		strings.Replace(validRecord("billing/invoice/other"), "lineage:", "metadata:\n  note: present\nlineage:", 1))
	write(t, filepath.Join(root, "apps/admin/other.go"), "// @facet: billing/invoice/other\n// @facet-role: primary\npackage admin\n")

	record = filepath.Join(root, "apps/storefront/docs/facets/features/billing/invoice/list.yaml")
	write(t, record, validRecord("billing/invoice/list"))
	write(t, filepath.Join(root, "apps/storefront/list.go"), "// @facet: billing/invoice/list\n// @facet-role: primary\npackage storefront\n")
	return root, record
}

// A record is judged by the registry that holds it and by no other. Running a
// sibling's schema over it faults every field the two do not share, which in a
// monorepo is most of them — and the author is then told to fix a record that
// is correct.
func TestARecordIsJudgedOnlyByTheRegistryThatHoldsIt(t *testing.T) {
	root, record := twoRegistriesWithDifferentSchemas(t)
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "own-registry-session"
	in.ToolInput.FilePath = record
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if len(ctx.registries) != 2 {
		t.Fatalf("want both registries adopted, got %d — the fixture would prove nothing", len(ctx.registries))
	}

	if out := captureHook(t, func() { hookPostToolUse(ctx, in) }); out != "" {
		t.Fatalf("storefront's record must be judged by storefront's schema alone, got %q", out)
	}
}

// The other direction, so the test above cannot pass by the check being off
// altogether: the holding registry's own schema does reach the record.
func TestTheHoldingRegistrysSchemaDoesReachTheRecord(t *testing.T) {
	root, record := twoRegistriesWithDifferentSchemas(t)
	// Storefront's own schema forbids unknown properties, so this is a fault
	// its registry raises rather than a sibling's.
	write(t, record, strings.Replace(validRecord("billing/invoice/list"), "lineage:", "not_a_facet: x\nlineage:", 1))
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())

	var in hookInput
	in.CWD = root
	in.SessionID = "own-schema-applies-session"
	in.ToolInput.FilePath = record
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}

	out := captureHook(t, func() { hookPostToolUse(ctx, in) })
	if !strings.Contains(out, "need fixing now") {
		t.Fatalf("want the holding registry's schema to fault the record, got %q", out)
	}
	// Named with its registry, or in a monorepo the author has to guess which
	// of two files with the same id is meant.
	if !strings.Contains(out, "[storefront]") {
		t.Fatalf("want the registry named in the report, got %q", out)
	}
}

// brokenMonorepo builds two registries of which exactly one fails lint, and
// returns the root plus the name of the failing registry.
func brokenMonorepo(t *testing.T) (root, failing string) {
	t.Helper()
	root = adoptedRepo(t, "billing/invoice/list")
	os.RemoveAll(filepath.Join(root, "docs"))
	write(t, filepath.Join(root, ".facetrc.yaml"), `
registries:
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin]
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront]
`)
	for _, reg := range []struct{ name, id, dir string }{
		{"admin", "billing/invoice/other", "apps/admin"},
		{"storefront", "billing/invoice/list", "apps/storefront"},
	} {
		write(t, filepath.Join(root, reg.dir, "docs/facets/features", reg.id+".yaml"), validRecord(reg.id))
		write(t, filepath.Join(root, reg.dir, "impl.go"),
			"// @facet: "+reg.id+"\n// @facet-role: primary\npackage p\n")
	}
	// The defect, in storefront alone: an anchor naming a record nobody wrote.
	write(t, filepath.Join(root, "apps/storefront/ghost.go"),
		"// @facet: billing/invoice/ghost\n// @facet-role: primary\npackage p\n")
	return root, "storefront"
}

// The staged candidate covers every registry.
func TestTheGateCoversEveryRegistryNotOnlyTheFirst(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root, failing := brokenMonorepo(t)
	ctx, in := preToolUseFor(t, root, "git commit -m wip")
	if len(ctx.registries) != 2 {
		t.Fatalf("want both registries adopted, got %d — the fixture would prove nothing", len(ctx.registries))
	}

	out := captureHook(t, func() { hookPreToolUse(ctx, in) })

	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("want the commit denied because one registry fails, got %q", out)
	}
	// Which registry is broken, or in a monorepo the author reads a denial and
	// has no idea which of several trees to look in.
	if !strings.Contains(out, "["+failing+"]") {
		t.Fatalf("want the failing registry named in the reason, got %q", out)
	}
	if !strings.Contains(out, "billing/invoice/ghost") {
		t.Fatalf("want the failing id in the reason, got %q", out)
	}
}

// In a monorepo a shared file is scanned by several registries, and its anchor
// resolves as long as one of them owns the id. Judging it against each registry
// separately calls every shared anchor broken — which is the reminder crying
// wolf in exactly the files most likely to be edited.
func TestASharedFilesAnchorResolvesThroughAnyClaimingRegistry(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), `
registries:
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin, packages/shared]
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront, packages/shared]
`)
	// The id exists in storefront alone.
	write(t, filepath.Join(root, "apps/storefront/docs/facets/features/billing/invoice/list.yaml"),
		validRecord("billing/invoice/list"))
	write(t, filepath.Join(root, "apps/admin/docs/facets/features/billing/invoice/other.yaml"),
		validRecord("billing/invoice/other"))
	write(t, filepath.Join(root, "apps/admin/impl.go"), "// @facet: billing/invoice/other\n// @facet-role: primary\npackage p\n")

	shared := filepath.Join(root, "packages/shared/list.go")
	write(t, shared, "// @facet: billing/invoice/list\n// @facet-role: primary\npackage shared\n")

	var in hookInput
	in.CWD = root
	in.SessionID = "shared-anchor-session"
	in.ToolInput.FilePath = shared
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	if len(ctx.registries) != 2 {
		t.Fatalf("want both registries adopted, got %d — the fixture would prove nothing", len(ctx.registries))
	}
	// The premise: both registries scan the shared file, and the first of them
	// is not the one that owns the id.
	claiming := ctx.sourceRegistries(shared)
	if len(claiming) != 2 {
		t.Fatalf("want both registries to claim the shared file, got %d", len(claiming))
	}
	if claiming[0].Name != "admin" {
		t.Fatalf("this test needs the non-owning registry first, got %q", claiming[0].Name)
	}

	out := captureHook(t, func() { hookCheckAnchors(ctx, in, shared) })

	if strings.Contains(out, "do not resolve") {
		t.Fatalf("a shared file's anchor must resolve through the registry that owns it, got %q", out)
	}
	if !strings.Contains(out, "implements FACET feature(s)") {
		t.Fatalf("want the ordinary reminder for a resolved anchor, got %q", out)
	}
}

// Mutation: remove the Bash changed-file scan from hookPostToolUse. The binary
// still builds, but this test loses both file names and the record digest.
func TestBashPostToolUseCarriesChangedAnchorsOnce(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml"), "schema_version: \"0.1\"\nid: demo/thing/widget\nlifecycle: active\nintent:\n  summary: Lists widgets.\ncontract:\n  scenarios:\n    - id: list-widgets\n      statement: Given widgets When listed Then they appear\n")
	for _, path := range []string{"src/a.ts", "src/b.ts"} {
		write(t, filepath.Join(root, path), "// @facet: demo/thing/widget\nexport const x = 1;\n")
	}
	var in hookInput
	in.ToolName = "Bash"
	in.SessionID = "bulk"
	in.CWD = root
	ctx := newHookContext(root)
	out := captureHook(t, func() { hookPostToolUse(ctx, in) })
	var envelope hookOutput
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("one JSON response required: %v (%s)", err, out)
	}
	for _, want := range []string{"src/a.ts", "src/b.ts", "Lists widgets.", "list-widgets"} {
		if !strings.Contains(envelope.HookSpecificOutput.AdditionalContext, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
	if got := captureHook(t, func() { hookPostToolUse(ctx, in) }); got != "" {
		t.Errorf("repeated Bash notice: %s", got)
	}
}

// Bash events cap the number of uncommitted anchored files they read and
// report, bounding the work repeated after every command in a large checkout.
func TestBashPostToolUseIsCappedOnALargeDirtyTree(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml"), "schema_version: \"0.1\"\nid: demo/thing/widget\nlifecycle: active\nintent:\n  summary: Lists widgets.\n")
	const files = 200
	for i := 0; i < files; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("src/f%03d.ts", i)), "// @facet: demo/thing/widget\nexport const x = 1;\n")
	}
	// The premise: every one of them is a changed file the event would see.
	if got := len(facet.ChangedFiles(root)); got < files {
		t.Fatalf("fixture must leave %d changed files, got %d", files, got)
	}
	var in hookInput
	in.ToolName = "Bash"
	in.SessionID = "cap"
	in.CWD = root
	out := captureHook(t, func() { hookPostToolUse(newHookContext(root), in) })
	var envelope hookOutput
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("one JSON response required: %v (%s)", err, out)
	}
	text := envelope.HookSpecificOutput.AdditionalContext
	if n := strings.Count(text, "implements FACET feature(s)"); n != bulkMaxFiles {
		t.Errorf("want exactly %d files reminded, got %d", bulkMaxFiles, n)
	}
	if !strings.Contains(text, fmt.Sprintf("only the first %d were", bulkMaxFiles)) {
		t.Errorf("the cap must be said, not silent: %s", text)
	}
}

// Mutation: scan all anchored files instead of ChangedFiles. A committed,
// unchanged source then emits context for a read-only Bash event.
func TestReadOnlyBashInCleanTreeIsSilent(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "src/a.ts"), "// @facet: demo/thing/widget\nexport const x = 1;\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	if changed := facet.ChangedFiles(root); len(changed) != 0 {
		t.Fatalf("fixture must be clean: %v", changed)
	}
	var in hookInput
	in.ToolName = "Bash"
	in.ToolInput.Command = "git status"
	if out := captureHook(t, func() { hookPostToolUse(newHookContext(root), in) }); out != "" {
		t.Fatalf("read-only Bash emitted: %s", out)
	}
}

// Mutation: use a constant anchor marker scope. Two adopted repositories
// then silence one another when their feature and source path are identical.
func TestAnchorRemindersAreScopedToRepository(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	var in hookInput
	in.SessionID = "shared"
	in.ToolName = "Bash"
	for i := 0; i < 2; i++ {
		root := adoptedRepo(t, "demo/thing/widget")
		write(t, filepath.Join(root, "src/same.ts"), "// @facet: demo/thing/widget\nexport const x = 1;\n")
		out := captureHook(t, func() { hookPostToolUse(newHookContext(root), in) })
		if !strings.Contains(out, "src/same.ts") {
			t.Fatalf("repo %d lost its own reminder: %s", i, out)
		}
	}
}

// Mutation: scope markers only to repository and bare ID. The beta qualifier
// and its independent reset are then suppressed by alpha's existing marker.
func TestAnchorRemindersAndResetAreScopedToOwningRegistry(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, ".facetrc.yaml"), "registries:\n  alpha:\n    records: alpha\n    sources: [src]\n  beta:\n    records: beta\n    sources: [src]\n")
	for _, name := range []string{"alpha", "beta"} {
		write(t, filepath.Join(root, name, "features/demo/thing/widget.yaml"), validRecord("demo/thing/widget"))
	}
	ctx := newHookContext(root)
	if len(ctx.registries) != 2 {
		t.Fatalf("fixture registries: %v", ctx.registries)
	}
	path := filepath.Join(root, "src/shared.ts")
	var in hookInput
	in.SessionID = "registries"
	in.ToolInput.FilePath = path
	run := func(name string) string {
		write(t, path, "// @facet: "+name+":demo/thing/widget\nexport const x = 1;\n")
		return captureHook(t, func() { hookPostToolUse(ctx, in) })
	}
	for _, name := range []string{"alpha", "beta"} {
		if out := run(name); !strings.Contains(out, name+":demo/thing/widget") {
			t.Fatalf("%s reminder lost: %s", name, out)
		}
	}
	if out := run("alpha"); out != "" {
		t.Fatalf("alpha duplicate: %s", out)
	}
	in.ToolInput.FilePath = filepath.Join(root, "alpha/features/demo/thing/widget.yaml")
	captureHook(t, func() { hookPostToolUse(ctx, in) })
	in.ToolInput.FilePath = path
	if out := run("alpha"); !strings.Contains(out, "alpha:demo/thing/widget") {
		t.Fatalf("alpha reset lost: %s", out)
	}
	if out := run("beta"); out != "" {
		t.Fatalf("alpha reset cleared beta too: %s", out)
	}
}

// A record save is judged on the record. Checks that read the whole history or
// report on directories never produce a finding the hook shows, and the history
// pass is a 'git log --diff-filter=A' over every commit — once per record
// written, which during an adoption is hundreds of times.
func TestRecordSaveDoesNotWalkTheHistory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git shim is a shell script")
	}
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	record := filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml")
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git is unavailable here: %v", err)
	}
	shimDir := t.TempDir()
	counter := filepath.Join(shimDir, "history.count")
	shim := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"--diff-filter=A\" ]; then printf 'x' >> " + strconv.Quote(counter) + "; fi\n" +
		"done\n" +
		"exec " + strconv.Quote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	count := func() int {
		data, _ := os.ReadFile(counter)
		return len(data)
	}

	// The premise: a full lint of this registry does walk the history, so a
	// zero below is the hook skipping it and not the shim missing the call.
	if _, err := facet.Lint(facet.Open(root), facet.LintOptions{}); err != nil {
		t.Fatal(err)
	}
	if count() == 0 {
		t.Fatal("the shim recorded no history walk for a full lint — it is not the git being run")
	}
	_ = os.Remove(counter)

	var in hookInput
	in.ToolName = "Write"
	in.SessionID = "save"
	in.CWD = root
	in.ToolInput.FilePath = record
	captureHook(t, func() { hookPostToolUse(newHookContext(root), in) })
	if n := count(); n != 0 {
		t.Fatalf("a record save walked the whole history %d time(s)", n)
	}
}
