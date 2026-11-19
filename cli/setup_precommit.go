package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() { setupSubcommands = append(setupSubcommands, newSetupPrecommitCmd) }

// precommitMarker is the line that says a hook file is FACET's. Everything
// that decides "may I overwrite or remove this?" asks for this line and for
// nothing else: a hook without it belongs to somebody, and is never touched.
const precommitMarker = "# facet-managed: pre-commit hook (written by 'facet setup precommit')"

// precommitScript is the hook. It calls facet by name and fails when facet is
// missing. A hook that passes because its binary is not on PATH looks exactly
// like a clean registry, and a commit made outside Claude is the one place
// nobody else is checking.
const precommitScript = `#!/bin/sh
` + precommitMarker + `
# Remove it with: facet setup precommit --remove
if ! command -v facet >/dev/null 2>&1; then
  echo "facet pre-commit: 'facet' is not on PATH, so the staged records and anchors were NOT checked." >&2
  echo "facet pre-commit: put facet on PATH, or remove this hook with 'facet setup precommit --remove'." >&2
  exit 1
fi
exec facet lint --staged
`

// foreignHookLines is what a person adds to a hook they already own.
const foreignHookLines = `command -v facet >/dev/null 2>&1 || { echo "facet is not on PATH: staged records were NOT checked" >&2; exit 1; }
facet lint --staged || exit 1`

// hookManager is a tool that owns the project's git hooks. When one is
// present FACET does not write a hook file beside it: the manager would
// either overwrite the file on its next install or run both.
type hookManager struct {
	name    string
	marker  []string // paths relative to the repository root, any of which means "present"
	snippet string
	where   string
}

var hookManagers = []hookManager{
	{
		name:    "husky",
		marker:  []string{".husky"},
		where:   "add this line to .husky/pre-commit",
		snippet: `facet lint --staged`,
	},
	{
		name:   "lefthook",
		marker: []string{"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml"},
		where:  "add this to the lefthook configuration",
		snippet: `pre-commit:
  commands:
    facet:
      run: facet lint --staged`,
	},
	{
		name:   "pre-commit",
		marker: []string{".pre-commit-config.yaml"},
		where:  "add this entry under 'repos:' in .pre-commit-config.yaml",
		snippet: `  - repo: local
    hooks:
      - id: facet-lint-staged
        name: facet lint --staged
        entry: facet lint --staged
        language: system
        pass_filenames: false
        always_run: true`,
	},
}

// detectHookManager returns the first hook manager the repository uses.
func detectHookManager(root string) (hookManager, bool) {
	for _, m := range hookManagers {
		for _, rel := range m.marker {
			if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
				return m, true
			}
		}
	}
	return hookManager{}, false
}

// gitHooksDir asks git where hooks live, which honours core.hooksPath and
// linked worktrees (whose hooks are the main checkout's). A path hard-coded to
// .git/hooks is wrong in both cases.
func gitHooksDir(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return "", fmt.Errorf("cannot ask git where hooks live: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("git returned no hooks directory")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir, nil
}

// newSetupPrecommitCmd installs a git pre-commit hook that runs
// 'facet lint --staged'. The plugin's commit gate only sees commits Claude
// makes through Bash; this is the check for the ones a person makes in a
// terminal.
//
// @facet: authoring/guardrails/local-commit-gate
// @facet-role: primary
func newSetupPrecommitCmd() *cobra.Command {
	var write, remove bool
	cmd := &cobra.Command{
		Use:   "precommit",
		Short: "Install a git pre-commit hook that runs 'facet lint --staged'",
		Long: `Wires 'facet lint --staged' into git's pre-commit hook, so a commit made in a
terminal is judged like the one Claude's commit gate judges: only the errors
the staged change introduces stop it.

Without options this is a dry run and writes nothing. --write installs the
hook into the directory git names for hooks (core.hooksPath and linked
worktrees are honoured). A hook that is already there and was not written by
FACET is never overwritten: the lines to add to it are printed instead. When a
hook manager (husky, lefthook, pre-commit) is present no hook file is written
at all; the entry for its configuration is printed.

The hook calls 'facet' by name and fails the commit, with a message, when
'facet' is not on PATH. --remove deletes the hook, and only if FACET wrote it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if write && remove {
				return errors.New("--write and --remove cannot be combined")
			}
			top := gitToplevel(ResolveRoot(cmd))
			if top == "" {
				return errors.New("not a git repository: there is no pre-commit hook to manage")
			}
			return runSetupPrecommit(cmd.OutOrStdout(), top, write, remove)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "install the hook (default: dry run)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the hook, only if FACET wrote it")
	return cmd
}

func runSetupPrecommit(out io.Writer, root string, write, remove bool) error {
	hooksDir, err := gitHooksDir(root)
	if err != nil {
		return err
	}
	hook := filepath.Join(hooksDir, "pre-commit")
	existing, readErr := os.ReadFile(hook)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("cannot read %s: %w", hook, readErr)
	}
	ours := exists && strings.Contains(string(existing), precommitMarker)

	if remove {
		switch {
		case !exists:
			fmt.Fprintf(out, "No pre-commit hook at %s; nothing to remove.\n", hook)
			return nil
		case !ours:
			return fmt.Errorf("%s was not written by FACET and is left alone", hook)
		}
		if err := os.Remove(hook); err != nil {
			return err
		}
		fmt.Fprintf(out, "Removed %s.\n", hook)
		return nil
	}

	if m, ok := detectHookManager(root); ok {
		// Nothing is written, with or without --write: see hookManager.
		fmt.Fprintf(out, "%s is present, so no git hook file is written. %s:\n\n%s\n", m.name, capitalise(m.where), m.snippet)
		return nil
	}

	if exists && !ours {
		fmt.Fprintf(out, "%s already exists and was not written by FACET, so it is left alone.\nAdd these lines to it:\n\n%s\n", hook, foreignHookLines)
		return errSilent
	}

	if !write {
		verb := "install"
		if ours {
			verb = "update"
		}
		fmt.Fprintf(out, "Dry run: would %s %s, which runs 'facet lint --staged' and fails the commit when 'facet' is not on PATH.\nRun with --write to do it.\n", verb, hook)
		return nil
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(hook, []byte(precommitScript), 0o755); err != nil {
		return err
	}
	// WriteFile keeps the mode of a file that already existed; the hook must be
	// executable or git skips it silently.
	if err := os.Chmod(hook, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s.\n", hook)
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
