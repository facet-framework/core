// @facet-ignore-file

package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanRecordYAML is the record behind src/list.ts in the fixtures below.
const cleanRecordYAML = "schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n"

// anchorOnly writes a source file whose anchor names a record nobody wrote:
// the one defect these fixtures use.
func anchorOnly(id string) string {
	return "// @facet: " + id + "\n// @facet-role: primary\nexport const x = 1;\n"
}

// cleanCommittedRepo is an adopted repository whose HEAD lints clean.
// The precondition is checked with the real binary, not assumed.
func cleanCommittedRepo(t *testing.T) string {
	t.Helper()
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), cleanRecordYAML)
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	if out, code := runFacet(t, root, nil, "lint"); code != 0 {
		t.Fatalf("fixture must lint clean, exit %d:\n%s", code, out)
	}
	return root
}

// runFacet runs the real binary in dir and returns its combined output and
// exit code. A binary that could not be started fails the test: an exit code
// is only evidence when the process actually ran.
func runFacet(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(facetBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("facet %s did not run: %v", strings.Join(args, " "), err)
	}
	return string(out), exit.ExitCode()
}

// Mutation: make --staged lint the working tree instead of the index (call
// facet.LintAll(root) in the --staged branch). The unstaged ghost anchor below
// is then reported and the exit code becomes 1.
func TestLintStagedIgnoresUnstagedBreakage(t *testing.T) {
	root := cleanCommittedRepo(t)
	write(t, filepath.Join(root, "src/unstaged.ts"), anchorOnly("billing/invoice/unstaged"))

	if out, code := runFacet(t, root, nil, "lint"); code != 1 || !strings.Contains(out, "billing/invoice/unstaged") {
		t.Fatalf("premise: the whole-repo lint must fail on the unstaged defect, exit %d:\n%s", code, out)
	}
	out, code := runFacet(t, root, nil, "lint", "--staged")
	if code != 0 {
		t.Fatalf("an unstaged defect blocked --staged, exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "billing/invoice/unstaged") {
		t.Fatalf("--staged named an unstaged defect:\n%s", out)
	}
}

// Mutation: drop the HEAD comparison in stagedLint (return candidate errors
// without subtracting the baseline). The ghost already in HEAD is then
// reported against a staged change that did not touch it.
func TestLintStagedReportsOnlyWhatTheIndexIntroduces(t *testing.T) {
	root := cleanCommittedRepo(t)
	write(t, filepath.Join(root, "src/old.ts"), anchorOnly("billing/invoice/old-debt"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "debt")
	if out, code := runFacet(t, root, nil, "lint"); code != 1 || !strings.Contains(out, "billing/invoice/old-debt") {
		t.Fatalf("premise: HEAD must carry the old error, exit %d:\n%s", code, out)
	}

	// A clean staged change on top of the debt passes.
	write(t, filepath.Join(root, "src/plain.ts"), "export const plain = 1;\n")
	git(t, root, "add", "src/plain.ts")
	out, code := runFacet(t, root, nil, "lint", "--staged")
	if code != 0 || strings.Contains(out, "old-debt") {
		t.Fatalf("existing debt blocked a clean staged change, exit %d:\n%s", code, out)
	}

	// A staged change that adds an error is reported, and only that one.
	write(t, filepath.Join(root, "src/fresh.ts"), anchorOnly("billing/invoice/fresh-defect"))
	git(t, root, "add", "src/fresh.ts")
	out, code = runFacet(t, root, nil, "lint", "--staged")
	if code != 1 || !strings.Contains(out, "billing/invoice/fresh-defect") {
		t.Fatalf("a new staged error passed, exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "old-debt") {
		t.Fatalf("--staged repeated the error HEAD already has:\n%s", out)
	}
}

// Mutation: build --json output from a different struct (or print null for an
// empty findings list). The decoded shape then differs from 'facet lint --json'.
func TestLintStagedJSONHasTheShapeOfLintJSON(t *testing.T) {
	root := cleanCommittedRepo(t)
	type shape struct {
		Findings []map[string]any `json:"findings"`
		Checked  struct {
			Records int `json:"records"`
		} `json:"checked"`
	}
	decode := func(args ...string) (shape, string, int) {
		out, code := runFacet(t, root, nil, args...)
		var s shape
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatalf("%v is not JSON: %v\n%s", args, err, out)
		}
		return s, out, code
	}

	// The fixture carries warnings, so the whole-repo report is not empty while
	// the staged one is: --staged reports errors only. That difference is what
	// gives the "[]" check below its meaning.
	whole, wholeRaw, _ := decode("lint", "--json")
	if len(whole.Findings) == 0 {
		t.Fatalf("premise: the whole-repo report must contain warnings:\n%s", wholeRaw)
	}
	clean, cleanRaw, code := decode("lint", "--staged", "--json")
	if code != 0 || !strings.Contains(cleanRaw, `"findings": []`) {
		t.Fatalf("clean --staged --json must print an empty array, not null, exit %d:\n%s", code, cleanRaw)
	}
	if clean.Checked.Records != whole.Checked.Records || clean.Checked.Records == 0 {
		t.Fatalf("--staged did not report what it checked: %d records, whole repo %d", clean.Checked.Records, whole.Checked.Records)
	}

	write(t, filepath.Join(root, "src/fresh.ts"), anchorOnly("billing/invoice/fresh-defect"))
	git(t, root, "add", "src/fresh.ts")
	bad, badRaw, code := decode("lint", "--staged", "--json")
	if code != 1 || len(bad.Findings) == 0 {
		t.Fatalf("broken --staged --json, exit %d:\n%s", code, badRaw)
	}
	for _, key := range []string{"severity", "check", "message"} {
		if _, ok := bad.Findings[0][key]; !ok {
			t.Fatalf("finding lacks %q:\n%s", key, badRaw)
		}
	}
}

// Mutation: let --staged fall back to the working directory when there is no
// repository (skip the gitToplevel check). It then lints a tree with no index
// and reports success.
func TestLintStagedWithoutARepositoryFails(t *testing.T) {
	dir := t.TempDir()
	out, code := runFacet(t, dir, []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}, "lint", "--staged")
	if code == 0 || !strings.Contains(out, "git repository") {
		t.Fatalf("--staged outside a repository must fail and say why, exit %d:\n%s", code, out)
	}
}

// Mutation: delete the symbolic-ref/unborn branch in stagedLint, so any failure
// to resolve HEAD becomes an empty baseline. Both damaged HEADs below then lint
// as clean instead of failing. A second mutation, resolving with
// 'rev-parse --verify HEAD' instead of 'HEAD^{commit}', makes the missing-object
// case pass as a baseline of nothing.
func TestLintStagedDamagedHeadIsNotACleanReport(t *testing.T) {
	cases := map[string]string{
		"the ref points at an object that does not exist": "0123456789012345678901234567890123456789\n",
		"the ref file holds garbage":                      "this is not a hash\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root := cleanCommittedRepo(t)
			// HEAD stays a symbolic ref; the branch it names is damaged. (A bare
			// SHA in .git/HEAD would be a valid detached HEAD to git.)
			ref := strings.TrimSpace(git(t, root, "symbolic-ref", "HEAD"))
			write(t, filepath.Join(root, ".git", ref), content)
			if _, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^{commit}").Output(); err == nil {
				t.Fatal("premise: HEAD must be damaged")
			}
			out, code := runFacet(t, root, nil, "lint", "--staged")
			if code == 0 || !strings.Contains(out, "resolve HEAD") {
				t.Fatalf("a damaged HEAD must fail --staged and say it cannot resolve HEAD, exit %d:\n%s", code, out)
			}
		})
	}
}

// Mutation: give an unborn branch an allowance (skip the candidate lint when
// HEAD does not resolve). The staged defect below then passes.
func TestLintStagedOnAnUnbornBranchHasAnEmptyBaseline(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), cleanRecordYAML)
	write(t, filepath.Join(root, "src/list.ts"), "// @facet: billing/invoice/list\n// @facet-role: primary\nexport const list = 1;\n")
	git(t, root, "add", ".")
	if _, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^{commit}").Output(); err == nil {
		t.Fatal("premise: the branch must be unborn")
	}
	if out, code := runFacet(t, root, nil, "lint", "--staged"); code != 0 {
		t.Fatalf("a clean initial commit was refused, exit %d:\n%s", code, out)
	}
	write(t, filepath.Join(root, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, root, "add", "src/ghost.ts")
	if out, code := runFacet(t, root, nil, "lint", "--staged"); code != 1 || !strings.Contains(out, "billing/invoice/ghost") {
		t.Fatalf("a defective initial commit passed, exit %d:\n%s", code, out)
	}
}

// Mutation: read the switch from the working tree (os.ReadFile of
// .facetrc.yaml instead of 'git show HEAD:.facetrc.yaml' in commitGateOffAt).
// The unstaged and the staged 'commit_gate: off' below then switch the gate off
// and the broken commit is no longer denied.
func TestTheCommitGateSwitchIsReadFromHeadOnly(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := cleanCommittedRepo(t)
	const off = "hooks:\n  commit_gate: off\n"
	denied := func(label, reason string) {
		t.Helper()
		got := gateFor(t, root, "git commit -m next")
		if !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, reason) {
			t.Fatalf("%s: the broken commit must be denied with %q: %s", label, reason, got)
		}
	}

	// Working tree only.
	write(t, filepath.Join(root, ".facetrc.yaml"), off)
	write(t, filepath.Join(root, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, root, "add", "src/ghost.ts")
	// An untracked configuration has no HEAD to compare with, so the gate refuses.
	denied("unstaged switch", "untracked configuration")

	// Staged next to the defect: the agent's own way around its gate. The
	// refusal is about the switch, whatever else is broken.
	git(t, root, "add", ".facetrc.yaml")
	denied("staged switch", "person running the session")

	// Committed switch: honoured for the commits after it.
	git(t, root, "reset", "-q", "src/ghost.ts")
	git(t, root, "commit", "-qm", "switch the gate off")
	git(t, root, "add", "src/ghost.ts")
	if got := gateFor(t, root, "git commit -m next"); got != "" {
		t.Fatalf("the committed switch was not honoured: %s", got)
	}
}

// Mutation: ignore the value (treat any hooks.commit_gate as off). A typo then
// disables the gate; here LoadConfig must refuse it and the gate stays on.
func TestAMisspelledGateSwitchLeavesTheGateOn(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := cleanCommittedRepo(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  commit_gate: of\n")
	git(t, root, "add", ".facetrc.yaml")
	git(t, root, "commit", "-qm", "typo")
	write(t, filepath.Join(root, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, root, "add", "src/ghost.ts")
	if got := gateFor(t, root, "git commit -m next"); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("a misspelled switch disabled the gate: %q", got)
	}
}

// The environment stays the per-session hatch: with the gate on in the
// configuration, FACET_SKIP_COMMIT_GATE=1 still opens it.
func TestTheEnvironmentStillOpensAGateThatIsOn(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "1")
	root := cleanCommittedRepo(t)
	write(t, filepath.Join(root, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, root, "add", "src/ghost.ts")
	if got := gateFor(t, root, "git commit -m next"); got != "" {
		t.Fatalf("the environment hatch no longer opens the gate: %s", got)
	}
}

// The two-step attack: step 1 stages ONLY the switch. Nothing is broken in that
// commit, so a gate that only lints would pass it and every later commit would
// be unguarded. The commit that turns the gate off is refused.
func TestAStagedSwitchAloneIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := cleanCommittedRepo(t)
	if got := gateFor(t, root, "git commit -m premise"); got != "" {
		t.Fatalf("premise: a clean index must pass the gate: %s", got)
	}
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  commit_gate: off\n")
	git(t, root, "add", ".facetrc.yaml")
	got := gateFor(t, root, "git commit -m next")
	if !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "person running the session") || !strings.Contains(got, "FACET_SKIP_COMMIT_GATE=1") {
		t.Fatalf("a lone staged switch must be refused with the reason: %s", got)
	}
}

// A switch at HEAD in repository A counts for A alone. Repository B has the gate
// on and a staged error; every route from A to B is refused or judged by B.
func TestASwitchAtHeadIsNeverBorrowedByAnotherRepository(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	a := cleanCommittedRepo(t)
	write(t, filepath.Join(a, ".facetrc.yaml"), "hooks:\n  commit_gate: off\n")
	git(t, a, "add", ".facetrc.yaml")
	git(t, a, "commit", "-qm", "switch off")
	b := cleanCommittedRepo(t)
	write(t, filepath.Join(b, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, b, "add", "src/ghost.ts")

	// Premises: A really is switched off, and B really is refused on its own.
	write(t, filepath.Join(a, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, a, "add", "src/ghost.ts")
	if got := gateFor(t, a, "git commit -m own"); got != "" {
		t.Fatalf("premise: the committed switch must silence A: %s", got)
	}
	if got := gateFor(t, b, "git commit -m own"); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("premise: B must be refused: %s", got)
	}

	for _, command := range []string{
		"GIT_DIR=" + b + "/.git GIT_WORK_TREE=" + b + " git commit -m x",
		"git --git-dir=" + b + "/.git --work-tree=" + b + " commit -m x",
		"pushd " + b + " && git commit -m x",
		"cd " + b + " && git commit -m x",
	} {
		got := gateFor(t, a, command)
		if !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Errorf("%q borrowed A's switch: %q", command, got)
		}
	}
}
