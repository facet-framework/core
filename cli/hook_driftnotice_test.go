package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// driftingRepo returns a committed, lint-clean repository whose anchored source
// changed without its record, so the Stop hook has something to report.
func driftingRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_DRIFT_NOTICE", "")
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := cleanCommittedRepo(t)
	return root
}

func stopFor(t *testing.T, root, session string) string {
	t.Helper()
	// Each call is a fresh session so deduplication cannot hide a notice.
	var in hookInput
	in.CWD = root
	in.SessionID = session
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	return captureHook(t, func() { hookStop(ctx, in) })
}

func drift(t *testing.T, root string) {
	t.Helper()
	f := filepath.Join(root, "src/list.ts")
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	write(t, f, string(data)+"export const more = 2;\n")
}

func commitSwitch(t *testing.T, root, yaml string) {
	t.Helper()
	write(t, filepath.Join(root, ".facetrc.yaml"), yaml)
	git(t, root, "add", ".facetrc.yaml")
	git(t, root, "commit", "-qm", "switch")
}

// Mutation: delete the driftNoticeOff check in hookStop. The premise passes
// and the switched-off runs below then print the block.
func TestTheDriftNoticeCanBeSwitchedOffByConfigurationOrEnvironment(t *testing.T) {
	root := driftingRepo(t)
	drift(t, root)

	if got := stopFor(t, root, "premise"); !strings.Contains(got, `"decision":"block"`) || !strings.Contains(got, "billing/invoice/list") {
		t.Fatalf("premise: the drift must be reported while the notice is on: %q", got)
	}

	// The environment alone.
	t.Setenv("FACET_SKIP_DRIFT_NOTICE", "1")
	if got := stopFor(t, root, "env"); got != "" {
		t.Fatalf("FACET_SKIP_DRIFT_NOTICE=1 did not silence the notice: %s", got)
	}

	// The environment wins over a configuration that says on.
	commitSwitch(t, root, "hooks:\n  drift_notice: on\n")
	if got := stopFor(t, root, "env-over-on"); got != "" {
		t.Fatalf("the environment did not win over drift_notice: on: %s", got)
	}

	// The configuration alone, committed.
	t.Setenv("FACET_SKIP_DRIFT_NOTICE", "")
	if got := stopFor(t, root, "on"); !strings.Contains(got, `"decision":"block"`) {
		t.Fatalf("premise: drift_notice: on must leave the notice on: %q", got)
	}
	commitSwitch(t, root, "hooks:\n  drift_notice: off\n")
	drift(t, root)
	if got := stopFor(t, root, "config"); got != "" {
		t.Fatalf("hooks.drift_notice: off did not silence the notice: %s", got)
	}
}

// Mutation: read the switch from the working tree. The uncommitted off below
// then silences the notice.
func TestTheDriftNoticeSwitchIsReadFromHeadOnly(t *testing.T) {
	root := driftingRepo(t)
	drift(t, root)
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  drift_notice: off\n")
	if got := stopFor(t, root, "worktree"); !strings.Contains(got, `"decision":"block"`) {
		t.Fatalf("an uncommitted switch silenced the notice: %q", got)
	}
}

// Mutation: drop the switch case for DriftNotice in loadConfig. "of" then
// loads and reads as neither on nor off.
func TestAMisspelledDriftNoticeSwitchIsRejected(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  drift_notice: of\n")
	if _, err := facet.LoadConfig(root); err == nil || !strings.Contains(err.Error(), "hooks.drift_notice") || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("a misspelled drift_notice must be rejected, got %v", err)
	}
	// And a committed typo leaves the notice on.
	repo := driftingRepo(t)
	commitSwitch(t, repo, "hooks:\n  drift_notice: of\n")
	drift(t, repo)
	if got := stopFor(t, repo, "typo"); !strings.Contains(got, `"decision":"block"`) {
		t.Fatalf("a misspelled switch silenced the notice: %q", got)
	}
}

// Mutation: delete the drift_notice refusal in commitLintWithSourceView. A
// lone staged switch is then a clean commit and passes.
func TestTheCommitGateRefusesACommitThatSwitchesTheDriftNoticeOff(t *testing.T) {
	root := driftingRepo(t)
	if got := gateFor(t, root, "git commit -m premise"); got != "" {
		t.Fatalf("premise: a clean index must pass the gate: %s", got)
	}
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  drift_notice: off\n")
	git(t, root, "add", ".facetrc.yaml")
	got := gateFor(t, root, "git commit -m next")
	if !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "hooks.drift_notice: off") || !strings.Contains(got, "person running the session") {
		t.Fatalf("the switch commit was not refused: %s", got)
	}
	// The person's own route stays open.
	t.Setenv("FACET_SKIP_COMMIT_GATE", "1")
	if got := gateFor(t, root, "git commit -m next"); got != "" {
		t.Fatalf("the environment hatch no longer opens the gate: %s", got)
	}
}

// A project whose commit gate is already off must not become a way to switch
// the notice off.
func TestAnAlreadyOffGateDoesNotLetTheDriftNoticeSwitchThrough(t *testing.T) {
	root := driftingRepo(t)
	commitSwitch(t, root, "hooks:\n  commit_gate: off\n")
	write(t, filepath.Join(root, ".facetrc.yaml"), "hooks:\n  commit_gate: off\n  drift_notice: off\n")
	git(t, root, "add", ".facetrc.yaml")
	if got := gateFor(t, root, "git commit -m next"); !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "drift_notice") {
		t.Fatalf("the switch commit was not refused: %s", got)
	}
}
