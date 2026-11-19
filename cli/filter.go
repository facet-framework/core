package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// filterAttributeLine is what 'facet filter install' writes into the clone's
// own attributes file: every path through the filter. Binaries and prose pass
// through it untouched — the filter's first act on any content without an
// anchor marker is to hand it back — so the breadth costs a pipe, never a
// byte.
const filterAttributeLine = "* filter=facet"

// @facet: registry/layout/private-anchors
// @facet-role: primary
func newFilterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "filter",
		Short: "Keep @facet: anchors in the working copy and out of every commit",
		Long: `Private anchors: tags that exist in your checkout and nowhere in git.

Git's clean/smudge filters are the mechanism. On the way from the working copy
to the index every anchor block is lifted out into a sidecar under .git/; on
the way from the index to the working copy it is put back where it stood. Git
only ever sees the cleaned content, so no diff, no commit, no push, no merge
and no rebase carries an anchor — and none can conflict on one, because the
lines are never there.

Placement is by content, not by line: a block sits above a particular line and
follows that line when a pull moves it. When a pull removes the line, the block
is kept in the sidecar and reported as orphaned rather than guessed into a
nearby spot. 'facet filter status' lists them.

Anchors the repository already commits are untouched. A block that stands in
HEAD is not private and passes through; only what you add is lifted out.

Everything the setup touches lives in the clone: .git/info/attributes,
.git/config, and .git/facet/anchors/. The repository changes by zero bytes.`,
	}
	cmd.PersistentFlags().String("repository", "", "logical repository in the selected source view")
	cmd.AddCommand(newFilterCleanCmd(), newFilterSmudgeCmd(), newFilterInstallCmd(), newFilterUninstallCmd(), newFilterStatusCmd(), newFilterRefreshCmd())
	return cmd
}

// newFilterRefreshCmd exists because of a shortcut inside git.
func newFilterRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh [path...]",
		Short: "Make 'git status' agree with 'git diff' after editing a private anchor",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, selectedSource, err := filterSelectedSource(cmd)
			if err != nil {
				return err
			}
			_ = selectedSource
			gitDir, err := facet.GitCommonDir(root)
			if err != nil {
				return fmt.Errorf("%s is not a git working tree: %w", root, err)
			}

			_ = gitDir
			paths := args
			if len(paths) == 0 {
				paths, err = statDirtyFiles(root)
				if err != nil {
					return err
				}
			}
			return refreshIndex(root, paths)
		},
	}
}

// statDirtyFiles lists the tracked files whose stat no longer matches the
// index — the only files the size shortcut can be wrong about.
func statDirtyFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-m", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// refreshIndex asks git to re-compare the named files by content, through the
// filter, and record their current stat. Always with an explicit pathspec, in
// batches that stay clear of the argument-length limit. Quiet: the exit status
// of --refresh reports "needs update" for files that really did change, which
// is information rather than failure.
func refreshIndex(root string, paths []string) error {
	const batch = 500
	for start := 0; start < len(paths); start += batch {
		end := start + batch
		if end > len(paths) {
			end = len(paths)
		}
		args := append([]string{"-C", root, "update-index", "-q", "--refresh", "--"}, paths[start:end]...)
		cmd := exec.Command("git", args...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}
	return nil
}

// filterRoot finds the working tree the filter is running in. Git runs clean
// and smudge from the top level, so the working directory is the answer nearly
// always; the rev-parse is for the times a shell or a test invokes the command
// from elsewhere.
func filterRoot() (string, error) {
	if wd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(wd, ".git")); statErr == nil {
			return wd, nil
		}
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git working tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// committedContent reads the file as HEAD has it, so the clean can tell a
// committed anchor from a private one. A path HEAD does not have — a new file,
// or a repository with no commit yet — has no committed anchors, and that is a
// normal answer rather than an error.
func committedContent(root, rel string) ([]byte, bool, error) {
	cmd := exec.Command("git", "-C", root, "show", "HEAD:"+rel)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, true, nil
	}
	msg := stderr.String()
	for _, absent := range []string{
		"does not exist in", "exists on disk, but not in", "Not a valid object name",
		"bad revision", "unknown revision", "invalid object name",
	} {
		if strings.Contains(msg, absent) {
			return nil, false, nil
		}
	}
	return nil, false, fmt.Errorf("git show HEAD:%s: %s", rel, strings.TrimSpace(msg))
}

// Git runs clean and smudge inside 'git add' and 'git checkout'; a project pin
// this build does not meet must not break those, so both are pin-exempt.
func newFilterCleanCmd() *cobra.Command {
	return markPinExempt(&cobra.Command{
		Use:    "clean <path>",
		Short:  "git clean filter: lift private anchors out of the content on its way to the index",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rel := filepath.ToSlash(args[0])
			content, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			// Every failure below falls open: the content goes through as it
			// came, and a message says so. Stripping anchors that then cannot
			// be saved would lose them; letting them through commits a
			// comment line. Only one of those is recoverable.
			passThrough := func(reason error) error {
				fmt.Fprintf(os.Stderr, "facet filter clean %s: %v — content passed through unchanged\n", rel, reason)
				_, werr := os.Stdout.Write(content)
				return werr
			}

			root, selectedSource, err := filterRuntimeSource(cmd)
			_ = selectedSource
			if err != nil {
				return passThrough(err)
			}
			committed, _, err := committedContent(root, rel)
			if err != nil {
				return passThrough(err)
			}
			clean, found := facet.StripPrivateAnchors(rel, content, committed)

			gitDir, err := facet.GitCommonDir(root)
			if err != nil {
				return passThrough(err)
			}
			previous, err := facet.LoadPrivateAnchors(gitDir, rel)
			if err != nil {
				return passThrough(err)
			}
			worktree, err := facet.PrivateAnchorWorktree(root)
			if err != nil {
				return passThrough(err)
			}
			repository := privateRepositoryID(root)
			revision := gitOutput(root, "rev-parse", "HEAD")
			contentHash := ""
			if selectedSource != nil {
				repository = selectedSource.Repository
				revision = selectedSource.Revision
				contentHash = selectedSource.ContentHash
			}
			facet.AttributePrivateAnchors(previous, found, worktree)
			merged := facet.MergeSidecar(previous, found, clean)
			if repository != "" {
				facet.AttributePrivateAnchorsInView(previous, found, worktree, repository, revision, contentHash)
				merged = facet.MergeSidecarInView(previous, found, clean, worktree, repository, revision)
			}
			if len(merged.Blocks) > 0 || len(previous.Blocks) > 0 {
				if err := facet.SavePrivateAnchors(gitDir, merged); err != nil {
					return passThrough(err)
				}
			}
			_, err = os.Stdout.Write(clean)
			return err
		},
	}, pinExemptSelf)
}

func newFilterSmudgeCmd() *cobra.Command {
	return markPinExempt(&cobra.Command{
		Use:    "smudge <path>",
		Short:  "git smudge filter: put private anchors back into the content on its way to the working copy",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rel := filepath.ToSlash(args[0])
			content, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			// A smudge that fails leaves the working copy half written, which
			// is worse than any anchor being late. Nothing here may error out:
			// on any trouble the content goes through as git handed it over.
			passThrough := func(reason error) error {
				if reason != nil {
					fmt.Fprintf(os.Stderr, "facet filter smudge %s: %v — content passed through unchanged\n", rel, reason)
				}
				_, werr := os.Stdout.Write(content)
				return werr
			}

			root, selectedSource, err := filterRuntimeSource(cmd)
			_ = selectedSource
			if err != nil {
				return passThrough(err)
			}
			gitDir, err := facet.GitCommonDir(root)
			if err != nil {
				return passThrough(err)
			}
			pa, err := facet.LoadPrivateAnchors(gitDir, rel)
			if err != nil {
				return passThrough(err)
			}
			if len(pa.Blocks) == 0 {
				return passThrough(nil)
			}
			// Read-only. The sidecar is shared by every worktree of the clone,
			// and whether a block has a place is a fact about this checkout
			// alone — recorded nowhere, recomputed by whoever asks.
			repository := privateRepositoryID(root)
			if selectedSource != nil {
				repository = selectedSource.Repository
			}
			out, orphans := facet.InjectAnchorsInView(content, pa.Blocks, repository)
			if len(orphans) > 0 {
				fmt.Fprintf(os.Stderr, "facet filter smudge %s: %d private anchor(s) have no place in this version and were kept aside — see 'facet filter status'\n", rel, len(orphans))
			}
			_, err = os.Stdout.Write(out)
			return err
		},
	}, pinExemptSelf)
}

// attributesPath asks git where this clone's own attributes file lives — for
// a linked worktree that is the common directory, not the worktree's.
func attributesPath(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/attributes").Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}

func gitConfig(root string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root, "config"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git config %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return nil
}

func newFilterInstallCmd() *cobra.Command {
	var command string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Configure this clone so anchors you add stay out of every commit",
		Long: `Writes three things, all inside the clone and none in the repository:

  .git/config             filter.facet.clean, .smudge, .required
  .git/info/attributes    '* filter=facet'
  .git/facet/anchors/     the sidecar, created on first use

'required' is set so a checkout or an add refuses to run rather than silently
skipping the filter if the facet binary goes missing — a filter that is
skipped once commits every private anchor in the files it touches.

Anchors the repository already commits are not affected: a block that stands in
HEAD passes through the filter as it is.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, selectedSource, err := filterSelectedSource(cmd)
			if err != nil {
				return err
			}
			_ = selectedSource
			if _, err := facet.GitDir(root); err != nil {
				return fmt.Errorf("%s is not a git working tree: %w", root, err)
			}
			if command == "" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				if resolved, err := filepath.EvalSymlinks(exe); err == nil {
					exe = resolved
				}
				command = exe
			}
			if selectedSource != nil {
				if existing := privateRepositoryID(root); existing != "" && existing != selectedSource.Repository {
					return fmt.Errorf("private anchors are already bound to logical repository %q", existing)
				}
				if err := gitConfig(root, "facet.privateRepository", selectedSource.Repository); err != nil {
					return err
				}
			}
			quoted := shellQuote(command)
			for _, kv := range [][2]string{
				{"filter.facet.clean", quoted + " filter clean %f"},
				{"filter.facet.smudge", quoted + " filter smudge %f"},
				{"filter.facet.required", "true"},
			} {
				if err := gitConfig(root, kv[0], kv[1]); err != nil {
					return err
				}
			}

			attrs, err := attributesPath(root)
			if err != nil {
				return err
			}
			existing, err := os.ReadFile(attrs)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if !containsLine(string(existing), filterAttributeLine) {
				if err := os.MkdirAll(filepath.Dir(attrs), 0o755); err != nil {
					return err
				}
				body := string(existing)
				if body != "" && !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
				body += filterAttributeLine + "\n"
				if err := os.WriteFile(attrs, []byte(body), 0o644); err != nil {
					return err
				}
			}

			commonDir, err := facet.GitCommonDir(root)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), `private anchors enabled for %s

  filter command   %s
  attributes       %s
  sidecar          %s

All three are shared by every worktree of this clone: one install serves them
all, and a private anchor added in one worktree stands in the others.

From now on an @facet: block you add on its own line stays in your working
copy and never reaches a commit. Anchors this repository already commits are
unaffected.

Three things to know:
  - After you add or edit a private anchor, 'git status' may show the file as
    modified while 'git diff' shows nothing: git judges by size before it
    consults the filter. 'git add <file>' or 'facet filter refresh' clears
    it; the plugin's write hook does so on its own.
  - The committed file has fewer lines than the one in your editor, by the
    number of anchor lines above any given point. Line numbers in CI output,
    stack traces and 'git blame' refer to the committed file.
  - 'facet lint' in a checkout without this sidecar — CI, a colleague's
    clone — sees no private anchors. A record anchored only privately lints
    clean here and reports as unanchored there.
`, root, command, attrs, filepath.Join(commonDir, facet.SidecarDirName))
			return nil
		},
	}
	cmd.Flags().StringVar(&command, "command", "", "the facet executable git should run (default: this binary's path)")
	return cmd
}

func newFilterUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the filter from this clone; private anchors stay in the working copy and become committable",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, selectedSource, err := filterSelectedSource(cmd)
			if err != nil {
				return err
			}
			_ = selectedSource
			for _, key := range []string{"filter.facet.clean", "filter.facet.smudge", "filter.facet.required"} {
				// --unset on a missing key is exit 5; a repository that never
				// had the key is already in the state this command produces.
				_ = gitConfig(root, "--unset", key)
			}
			attrs, err := attributesPath(root)
			if err != nil {
				return err
			}
			if existing, err := os.ReadFile(attrs); err == nil {
				var kept []string
				for _, l := range strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n") {
					if strings.TrimSpace(l) != filterAttributeLine {
						kept = append(kept, l)
					}
				}
				body := strings.Join(kept, "\n")
				if body != "" {
					body += "\n"
				}
				if err := os.WriteFile(attrs, []byte(body), 0o644); err != nil {
					return err
				}
			}
			gitDir, err := facet.GitCommonDir(root)
			if err != nil {
				return err
			}
			all, _ := facet.ListPrivateAnchors(gitDir)
			n := 0
			for _, pa := range all {
				n += len(pa.Blocks)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "private anchors disabled for %s\n", root)
			if n > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), `
%d private anchor(s) in %d file(s) are still in your working copy and are now
ordinary content: the next 'git add' of those files will stage them. The
sidecar under %s was left in place in case you re-enable the filter.
`, n, len(all), filepath.Join(gitDir, facet.SidecarDirName))
			}
			return nil
		},
	}
}

func newFilterStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List the private anchors this clone keeps, and any that lost their place",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, selectedSource, err := filterSelectedSource(cmd)
			if err != nil {
				return err
			}
			_ = selectedSource
			gitDir, err := facet.GitCommonDir(root)
			if err != nil {
				return fmt.Errorf("%s is not a git working tree: %w", root, err)
			}
			all, err := facet.ListPrivateAnchors(gitDir)
			if err != nil {
				return err
			}
			worktree, err := facet.PrivateAnchorWorktree(root)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if selectedSource != nil {
				fmt.Fprintf(w, "repository %s; revision %s; snapshot %s\n", selectedSource.Repository, selectedSource.Revision, selectedSource.ContentHash)
			} else {
				id := privateRepositoryID(root)
				if id == "" {
					id = "unknown"
				}
				fmt.Fprintf(w, "repository %s; revision %s\n", id, gitOutput(root, "rev-parse", "HEAD"))
			}
			if len(all) == 0 {
				fmt.Fprintln(w, "no private anchors in this clone")
				return nil
			}
			total, orphaned, foreign, unknown := 0, 0, 0, 0
			for _, pa := range all {
				// Judged against this worktree's file: the sidecar is shared,
				// the answer is not.
				working, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(pa.Path)))
				missing := map[string]bool{}
				for _, o := range pa.Orphans(working) {
					missing[strings.Join(o.Lines, "\n")] = true
				}
				fmt.Fprintf(w, "%s\n", pa.Path)
				for _, b := range pa.Blocks {
					total++
					first := strings.TrimSpace(b.Lines[0])
					if selectedSource != nil && b.Repository != "" && b.Repository != selectedSource.Repository {
						fmt.Fprintf(w, "  ? %s (unresolved: association belongs to repository %s; retained)\n", first, b.Repository)
						unknown++
						continue
					}
					if b.Repository == "" || b.OriginRevision == "" {
						fmt.Fprintln(w, "    origin repository/revision unknown (legacy association)")
					} else {
						fmt.Fprintf(w, "    origin repository %s; revision %s\n", b.Repository, b.OriginRevision)
					}
					switch {
					case missing[strings.Join(b.Lines, "\n")] && b.OriginWorktree == "":
						unknown++
						fmt.Fprintf(w, "  ? %s   (absent here; origin worktree unknown — was above %q)\n", first, b.Before)
					case missing[strings.Join(b.Lines, "\n")] && b.OriginWorktree != worktree:
						foreign++
						fmt.Fprintf(w, "  ~ %s   (absent here; originated in worktree %q — was above %q)\n", first, b.OriginWorktree, b.Before)
					case missing[strings.Join(b.Lines, "\n")]:
						orphaned++
						fmt.Fprintf(w, "  ! %s   (no place in the current file — was above %q)\n", first, b.Before)
					case b.AtEnd:
						fmt.Fprintf(w, "    %s   (end of file)\n", first)
					default:
						fmt.Fprintf(w, "    %s   (above %q)\n", first, b.Before)
					}
				}
			}
			fmt.Fprintf(w, "\n%d private anchor(s) in %d file(s)", total, len(all))
			if orphaned > 0 {
				fmt.Fprintf(w, ", %d without a place from this worktree — review their placement; keep the shared sidecar", orphaned)
			}
			if foreign > 0 {
				fmt.Fprintf(w, ", %d absent here from another worktree", foreign)
			}
			if unknown > 0 {
				fmt.Fprintf(w, ", %d absent here with unknown origin", unknown)
			}
			if orphaned+foreign+unknown > 0 {
				fmt.Fprint(w, "; unresolved associations retained")
			}
			fmt.Fprintln(w)
			return nil
		},
	}
}

func containsLine(body, line string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// shellQuote makes a path safe inside the filter command git will hand to a
// shell. Paths with spaces are ordinary on macOS.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
