// @facet-ignore-file
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hookPath(t *testing.T, root string) string {
	t.Helper()
	dir, err := gitHooksDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "pre-commit")
}

// withFacetOnPath returns an environment whose PATH starts with the directory
// of the real binary, so a hook that calls "facet" by name finds it.
func withFacetOnPath(t *testing.T) []string {
	t.Helper()
	return []string{"PATH=" + filepath.Dir(facetBinary(t)) + string(os.PathListSeparator) + os.Getenv("PATH")}
}

// gitCommit runs a real 'git commit' so the hook is run by git itself, and
// returns its output and exit code.
func gitCommit(t *testing.T, root string, env []string) (string, int) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "commit", "-qm", "change")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("git commit did not run: %v", err)
	}
	return string(out), exit.ExitCode()
}

// Mutation: make runSetupPrecommit write the hook when --write is absent. The
// dry run then leaves a file behind.
func TestPrecommitDryRunWritesNothing(t *testing.T) {
	root := cleanCommittedRepo(t)
	out, code := runFacet(t, root, nil, "setup", "precommit")
	if code != 0 || !strings.Contains(out, "Dry run") {
		t.Fatalf("dry run, exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(hookPath(t, root)); !os.IsNotExist(err) {
		t.Fatalf("a dry run left a hook behind (stat error: %v)", err)
	}
}

// Mutation: write the hook without the executable bit (0o644). git then skips
// it and the broken commit goes through.
func TestPrecommitHookRefusesABrokenStagedCommitAndPassesACleanOne(t *testing.T) {
	root := cleanCommittedRepo(t)
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--write"); code != 0 {
		t.Fatalf("--write, exit %d:\n%s", code, out)
	}
	info, err := os.Stat(hookPath(t, root))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("the hook must exist and be executable: %v %v", info, err)
	}
	env := withFacetOnPath(t)

	// Unrelated debt that is not staged must not matter; a staged defect must.
	write(t, filepath.Join(root, "src/ghost.ts"), anchorOnly("billing/invoice/ghost"))
	git(t, root, "add", "src/ghost.ts")
	out, code := gitCommit(t, root, env)
	if code == 0 || !strings.Contains(out, "billing/invoice/ghost") {
		t.Fatalf("the hook let a broken staged record through, exit %d:\n%s", code, out)
	}
	if head := git(t, root, "rev-list", "--count", "HEAD"); strings.TrimSpace(head) != "1" {
		t.Fatalf("the refused commit was recorded anyway: %s commits", head)
	}

	git(t, root, "reset", "-q", "src/ghost.ts")
	write(t, filepath.Join(root, "src/plain.ts"), "export const plain = 1;\n")
	git(t, root, "add", "src/plain.ts")
	if out, code := gitCommit(t, root, env); code != 0 {
		t.Fatalf("the hook refused a clean staged commit, exit %d:\n%s", code, out)
	}
}

// Mutation: make the script skip the 'command -v facet' guard (delete it, so
// 'exec facet' alone runs, or change the guard's 'exit 1' to 'exit 0'). With
// facet missing the broken commit must be refused; the mutation lets it pass.
func TestPrecommitHookFailsWhenFacetIsMissing(t *testing.T) {
	root := cleanCommittedRepo(t)
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--write"); code != 0 {
		t.Fatalf("--write, exit %d:\n%s", code, out)
	}
	write(t, filepath.Join(root, "src/plain.ts"), "export const plain = 1;\n")
	git(t, root, "add", "src/plain.ts")

	// A PATH with git and sh but no facet. The premise is checked.
	bin := t.TempDir()
	for _, tool := range []string{"git", "sh"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	probe := exec.Command("sh", "-c", "command -v facet")
	probe.Env = []string{"PATH=" + bin}
	if err := probe.Run(); err == nil {
		t.Fatal("premise: facet must not be found on the restricted PATH")
	}
	out, code := gitCommit(t, root, []string{"PATH=" + bin})
	if code == 0 {
		t.Fatalf("a commit passed a hook whose binary is missing:\n%s", out)
	}
	if !strings.Contains(out, "not on PATH") {
		t.Fatalf("the hook did not say why it failed:\n%s", out)
	}
}

// Mutation: skip the marker check and overwrite whatever hook exists. The
// foreign hook's bytes are then replaced.
func TestPrecommitNeverOverwritesAForeignHook(t *testing.T) {
	root := cleanCommittedRepo(t)
	hook := hookPath(t, root)
	const foreign = "#!/bin/sh\necho my own check\n"
	write(t, hook, foreign)
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := runFacet(t, root, nil, "setup", "precommit", "--write")
	if code == 0 {
		t.Fatalf("--write over a foreign hook must not report success:\n%s", out)
	}
	if !strings.Contains(out, "facet lint --staged") || !strings.Contains(out, "not written by FACET") {
		t.Fatalf("the refusal must print the line to add:\n%s", out)
	}
	if got, _ := os.ReadFile(hook); string(got) != foreign {
		t.Fatalf("the foreign hook was changed:\n%s", got)
	}

	// --remove must not delete it either.
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--remove"); code == 0 {
		t.Fatalf("--remove deleted or accepted a foreign hook:\n%s", out)
	}
	if got, _ := os.ReadFile(hook); string(got) != foreign {
		t.Fatalf("--remove touched the foreign hook:\n%s", got)
	}
}

// Mutation: drop detectHookManager from runSetupPrecommit. A hook file is then
// written next to husky, and the test sees it.
func TestPrecommitWritesNoHookBesideAHookManager(t *testing.T) {
	cases := map[string]struct{ path, snippet string }{
		"husky":      {".husky/pre-commit", "facet lint --staged"},
		"lefthook":   {"lefthook.yml", "run: facet lint --staged"},
		"pre-commit": {".pre-commit-config.yaml", "entry: facet lint --staged"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := cleanCommittedRepo(t)
			write(t, filepath.Join(root, c.path), "# the manager's own file\n")
			out, code := runFacet(t, root, nil, "setup", "precommit", "--write")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if !strings.Contains(out, name) || !strings.Contains(out, c.snippet) {
				t.Fatalf("the snippet for %s was not printed:\n%s", name, out)
			}
			if _, err := os.Stat(hookPath(t, root)); !os.IsNotExist(err) {
				t.Fatalf("a git hook was written beside %s (stat error: %v)", name, err)
			}
		})
	}
}

// Mutation: have --remove delete the file whatever it contains (drop the
// 'ours' check). The foreign case above catches it; this case checks the other
// half: a hook FACET wrote is really removed.
func TestPrecommitRemoveDeletesTheHookItWrote(t *testing.T) {
	root := cleanCommittedRepo(t)
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--write"); code != 0 {
		t.Fatalf("--write, exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(hookPath(t, root)); err != nil {
		t.Fatalf("premise: the hook must exist: %v", err)
	}
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--remove"); code != 0 {
		t.Fatalf("--remove, exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(hookPath(t, root)); !os.IsNotExist(err) {
		t.Fatalf("the hook is still there (stat error: %v)", err)
	}
}

// Mutation: hard-code <root>/.git/hooks instead of asking git. With
// core.hooksPath set, the hook lands where git never looks and the broken
// commit passes.
func TestPrecommitHonoursCoreHooksPath(t *testing.T) {
	root := cleanCommittedRepo(t)
	custom := filepath.Join(root, "custom-hooks")
	git(t, root, "config", "core.hooksPath", custom)
	if out, code := runFacet(t, root, nil, "setup", "precommit", "--write"); code != 0 {
		t.Fatalf("--write, exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "pre-commit")); err != nil {
		t.Fatalf("the hook is not in core.hooksPath: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git/hooks/pre-commit")); !os.IsNotExist(err) {
		t.Fatalf("a hook was also written to .git/hooks (stat error: %v)", err)
	}
}
