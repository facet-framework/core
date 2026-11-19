package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newSplitCmd reports what a re-cut dropped.
//
// @facet: registry/layout/recut-loss-detector
// @facet-role: primary
func newSplitCmd() *cobra.Command {
	var check bool
	var asJSON bool
	var from string
	var registry string

	cmd := &cobra.Command{
		Use:   "split --check <parent-id> <child-id> [<child-id> ...]",
		Short: "Report what a re-cut dropped: facets the parent carried and no child does",
		Long: `Compares a record against the records it was split into.

After a split the parent's file is gone, so the old record is read from git —
by default from HEAD, which is the last commit before the working tree. Pass
--from to read it from another revision, or point it at a file.

What counts as a loss goes one level deeper than facet names where the name
alone proves nothing. A child that declares an empty lineage block has kept the
name and lost every edge, and that is the loss this check looks for.

Never a loss: scenarios (that is the unit you split along), id, metadata,
authorship, lifecycle, intent — each child writes its own.

The exit code is 0 whether or not something was lost. This reports.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !check {
				return fmt.Errorf("only --check exists today; splitting itself is still done by hand")
			}

			root := ResolveRoot(cmd)
			cfg, err := configForCommand(cmd)
			if err != nil {
				return err
			}
			// A monorepo can hold several registries, but a split happens
			// inside one: the parent and its children share an id space by
			// definition. --registry names it when the default is wrong.
			reg, err := cfg.Select(registry)
			if err != nil {
				return err
			}

			parentID, childIDs := args[0], args[1:]

			parentYAML, source, err := readParent(root, reg, parentID, from)
			if err != nil {
				return err
			}

			children := map[string][]byte{}
			for _, id := range childIDs {
				path := reg.PathForID(id)
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("reading the child %s: %w", id, err)
				}
				children[id] = data
			}

			report, err := facet.CheckSplit(parentYAML, children)
			if err != nil {
				return err
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (read from %s)\n", report.Parent, source)
			fmt.Fprintf(out, "split into %s\n\n", strings.Join(report.Children, ", "))

			if len(report.Losses) == 0 {
				fmt.Fprintln(out, "Nothing the parent carried is missing from all of the children.")
				return nil
			}

			fmt.Fprintf(out, "%d thing(s) the parent carried and no child does:\n\n", len(report.Losses))
			for _, l := range report.Losses {
				fmt.Fprintf(out, "  %s\n", l)
			}
			fmt.Fprintln(out, "\nEach of these was written by somebody about this feature. Losing one is a")
			fmt.Fprintln(out, "decision worth making on purpose rather than by not noticing.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "report what the split dropped")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&from, "from", "HEAD", "git revision, or a path, to read the parent record from")
	cmd.Flags().StringVar(&registry, "registry", "", "which registry, in a monorepo that declares several")
	return cmd
}

// readParent finds the old record. After a split its file no longer exists, so
// the default is git rather than the working tree — and the revision is named
// in the output, because a report that silently read the wrong version would be
// worse than no report.
func readParent(root string, reg facet.Registry, id, from string) ([]byte, string, error) {
	// A path wins over a revision: somebody who kept a copy aside should be
	// able to point at it without arguing with git.
	if info, err := os.Stat(from); err == nil && !info.IsDir() {
		data, err := os.ReadFile(from)
		return data, from, err
	}

	rel, err := filepath.Rel(root, reg.PathForID(id))
	if err != nil {
		return nil, "", fmt.Errorf("locating %s: %w", id, err)
	}
	spec := from + ":" + filepath.ToSlash(rel)

	out, err := exec.Command("git", "-C", root, "show", spec).Output()
	if err == nil {
		return out, spec, nil
	}

	// git could not produce it. Before blaming the revision, check whether the
	// file is simply still there — a split that has not been committed yet is
	// the ordinary case, and telling that person "git does not have it" would
	// send them looking in the wrong place.
	if data, ferr := os.ReadFile(reg.PathForID(id)); ferr == nil {
		return data, rel + " (working tree; not committed yet)", nil
	}
	return nil, "", fmt.Errorf("neither %s nor the working tree has %s — pass --from with the revision that still had it", spec, id)
}
