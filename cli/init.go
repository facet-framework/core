package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/facet-framework/core/cli/scaffold"
	"github.com/facet-framework/core/release"
	"github.com/spf13/cobra"
)

// newInitCmd scaffolds FACET into a project from the payload this binary
// carries. It needs no source checkout, no plugin and no network.
//
// @facet: delivery/distribution/project-scaffold
// @facet-role: related
func newInitCmd(identity release.Identity) *cobra.Command {
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "init [DIR]",
		Short: "Scaffold FACET into a project: record layout, schemas, spec, lint suite and CI workflow",
		Long: `Scaffold FACET into a project: the record layout, the JSON schemas, the
specification, the lint suite with its pinned dispatcher, an empty
.facetignore and the CI workflow.

DIR defaults to the repository root (--root, else the git toplevel, else the
current directory). Files the project already has are kept unless --force is
given, so a second run writes nothing. FACET_RECORDS_DIR selects another
record directory than docs/facets.

What needs judgement is left undone and named in the closing report: the
contents of .facetignore and the first record.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			if len(args) == 1 {
				root = args[0]
			}
			_, err := scaffold.Run(scaffold.Options{
				Root:       root,
				RecordsDir: os.Getenv("FACET_RECORDS_DIR"),
				DryRun:     dryRun,
				Force:      force,
				Version:    pinnedVersion(identity),
				Out:        cmd.OutOrStdout(),
			})
			var target *scaffold.TargetError
			if errors.As(err, &target) {
				return errors.New("init: " + target.Reason)
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print what would be written and write nothing")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace files the project already has")
	return cmd
}

// pinnedVersion is the version a scaffolded dispatcher installs. A release
// build pins itself, so the project's CI runs the version that scaffolded it;
// its build metadata is not part of a release tag. A development build has no
// release to download and keeps the pin the payload carries.
func pinnedVersion(identity release.Identity) string {
	switch identity.Channel {
	case "rc", "stable":
		version, _, _ := strings.Cut(identity.Version, "+")
		return version
	}
	return ""
}
