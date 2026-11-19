// @facet-ignore-file
package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Mutation: replace the baseline subtraction with all candidate errors. This
// compiles and denies the unchanged broken registry below.
func TestCommitRatchetAllowsDebtAndBlocksNewStagedErrors(t *testing.T) {
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	run := func() string {
		ctx, in := preToolUseFor(t, root, "git commit -m next")
		return captureHook(t, func() { hookPreToolUse(ctx, in) })
	}
	if got := run(); got != "" {
		t.Fatalf("unchanged debt blocked: %s", got)
	}
	write(t, filepath.Join(root, "src/new.ts"), "// @facet: billing/invoice/new\n// @facet-role: primary\nexport const n = 1;\n")
	if got := run(); got != "" {
		t.Fatalf("unstaged defect blocked index: %s", got)
	}
	git(t, root, "add", "src/new.ts")
	// The working file is now clean, but the defective blob remains staged.
	write(t, filepath.Join(root, "src/new.ts"), "export const n = 1;\n")
	if got := run(); !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "billing/invoice/new") {
		t.Fatalf("new staged error passed: %s", got)
	}
}

// Mutation: ignore errors returned by the HEAD lint; the broken configuration
// below then becomes a silent empty baseline.
func TestCommitRatchetCannotReadBaseline(t *testing.T) {
	root := brokenRegistry(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), "registries: [broken\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	write(t, filepath.Join(root, ".facetrc.yaml"), "sources: [src]\n")
	git(t, root, "add", ".")
	ctx, in := preToolUseFor(t, root, "git commit -m next")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "baseline") {
		t.Fatalf("unreadable baseline passed: %s", got)
	}
}

// Mutation: compare error counts instead of error identities. Replacing an old
// error with a new one in the same file then passes with an unchanged count.
func TestCommitRatchetReplacementIsANewError(t *testing.T) {
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	write(t, filepath.Join(root, "src/ghost.ts"), "// @facet: billing/invoice/replacement\n// @facet-role: primary\nexport const ghost = 1;\n")
	git(t, root, "add", ".")
	ctx, in := preToolUseFor(t, root, "git commit -m next")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, "billing/invoice/replacement") || !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("replacement error passed: %s", got)
	}
}

// Mutation: use git archive to materialize the candidate. export-ignore hides
// the staged defect and the gate no longer denies the commit.
func TestCommitRatchetIncludesExportIgnoredFiles(t *testing.T) {
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	write(t, filepath.Join(root, ".gitattributes"), "src/new.ts export-ignore\n")
	write(t, filepath.Join(root, "src/new.ts"), "// @facet: billing/invoice/new\n// @facet-role: primary\nexport const n=1;\n")
	git(t, root, "add", ".")
	ctx, in := preToolUseFor(t, root, "git commit -m next")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, "billing/invoice/new") {
		t.Fatalf("export-ignored error disappeared: %s", got)
	}
}

// Mutation: return an empty report when a private config is absent from HEAD.
// A private registry then receives a false claim that its index was checked.
func TestCommitRatchetRefusesPrivateConfiguration(t *testing.T) {
	root := brokenRegistry(t)
	write(t, filepath.Join(root, ".git/info/exclude"), ".facetrc.yaml\n")
	write(t, filepath.Join(root, ".facetrc.yaml"), "sources: [src]\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	ctx, in := preToolUseFor(t, root, "git commit -m next")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, "private") || !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("private config passed: %s", got)
	}
}

// Mutation: skip selection validation and lint the index for every invocation.
// A path commit or git add && commit can then publish an unexamined tree.
func TestCommitRatchetRejectsUnknownSelection(t *testing.T) {
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	for _, command := range []string{"git commit -am next", "git commit src/ghost.ts", "git add . && git commit -m next", "git status;git commit -m next", "git status&&git commit -m next", `git status;git commit -m "$MSG"`} {
		ctx, in := preToolUseFor(t, root, command)
		if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("unknown selection %q passed: %s", command, got)
		}
	}
}

// Mutation: require working-tree adoption before dispatching the commit hook.
// Removing the records directory then hides the orphan source anchor.
func TestCommitRatchetSurvivesRegistryDeletion(t *testing.T) {
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	git(t, root, "rm", "-r", "docs/facets")
	ctx, in := preToolUseFor(t, root, "git commit -m remove-registry")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "billing/invoice/list") {
		t.Fatalf("registry deletion bypassed gate: %s", got)
	}
}

// gateFor runs the pre-tool-use hook the way the binary does: the payload's
// cwd and command decide the repository, nothing else.
func gateFor(t *testing.T, cwd, command string) string {
	t.Helper()
	var in hookInput
	in.CWD = cwd
	in.SessionID = "gate-cd"
	in.ToolName = "Bash"
	in.ToolInput.Command = command
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		return ""
	}
	return captureHook(t, func() { hookPreToolUse(ctx, in) })
}

// An agent working in a FACET project commits in another repository by
// changing into it first. That commit is not this project's: it was blocked
// with the session repository's findings, because the gate never read the cd.
func TestCommitInAnotherRepositoryIsNotThisProjectsGate(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	session := brokenRegistry(t)
	git(t, session, "add", ".")
	git(t, session, "commit", "-qm", "baseline")
	write(t, filepath.Join(session, "src/new.ts"), "// @facet: billing/invoice/new\nexport const n = 1;\n")
	git(t, session, "add", ".")
	t.Setenv("CLAUDE_PROJECT_DIR", session)

	foreign := t.TempDir()
	git(t, foreign, "init", "-q")
	write(t, filepath.Join(foreign, "a.txt"), "hi\n")
	git(t, foreign, "add", ".")

	// The premise: the session repository's own commit is denied, so silence
	// below is the cd being read and not a gate that never fires.
	if got := gateFor(t, session, "git commit -m next"); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("the session repository must be denied: %s", got)
	}
	for _, command := range []string{
		"cd " + foreign + " && git commit -m x",
		"cd '" + foreign + "' && git -c user.name=x commit -qm x",
		"cd " + foreign + "; git commit -m x",
	} {
		if got := gateFor(t, session, command); got != "" {
			t.Errorf("%q was judged by the session repository: %s", command, got)
		}
	}
}

// Changing into a FACET repository does not get a commit past its gate: the
// compound command is refused as it always was, now against the right tree.
func TestCdIntoAnAdoptedRepositoryStillMeetsItsGate(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	target := brokenRegistry(t)
	git(t, target, "add", ".")
	git(t, target, "commit", "-qm", "baseline")
	elsewhere := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", elsewhere)
	if got := gateFor(t, elsewhere, "cd "+target+" && git commit -m next"); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("a cd into an adopted repository skipped its gate: %s", got)
	}
}

// The deny message tells the reader how to skip the gate. An agent cannot set
// its hook's environment, and a 'FACET_SKIP_COMMIT_GATE=1 git commit' prefix
// is refused on purpose, so the message must not offer it as the way.
func TestTheEscapeHatchIsDescribedTruthfully(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	write(t, filepath.Join(root, "src/new.ts"), "// @facet: billing/invoice/new\nexport const n = 1;\n")
	git(t, root, "add", ".")
	for _, command := range []string{"git commit -m next", "FACET_SKIP_COMMIT_GATE=1 git commit -m next"} {
		got := gateFor(t, root, command)
		if !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("%q must be denied: %s", command, got)
		}
		if !strings.Contains(got, "environment Claude Code was started with") {
			t.Errorf("%q: the escape hatch must say it belongs to the session environment: %s", command, got)
		}
	}
}

// Mutation: parse the working configuration before reading the candidate.
// A malformed unstaged configuration then blocks an unchanged staged tree.
func TestCommitRatchetIgnoresUnstagedConfigurationDamage(t *testing.T) {
	root := brokenRegistry(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), "sources: [src]\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	write(t, filepath.Join(root, ".facetrc.yaml"), "sources: [broken\n")
	ctx, in := preToolUseFor(t, root, "git commit -m next")
	if got := captureHook(t, func() { hookPreToolUse(ctx, in) }); got != "" {
		t.Fatalf("unstaged config affected commit: %s", got)
	}
}
