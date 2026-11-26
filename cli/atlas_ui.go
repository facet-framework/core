package cli

import (
	"fmt"

	"github.com/facet-framework/core/internal/atlaslauncher"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

const atlasLauncherAnnotation = "facet.atlas-launcher"

// @facet: delivery/distribution/atlas-launcher
// @facet-role: primary
func newAtlasUICmd(version string) *cobra.Command {
	launcher := atlaslauncher.New(version)
	var projectPin *facet.CorePin
	cmd := &cobra.Command{
		Use:                "ui",
		Short:              "Download and start FACET Atlas",
		DisableFlagParsing: true,
		Annotations:        map[string]string{atlasLauncherAnnotation: "true"},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			selection, start, err := atlaslauncher.StartSelection(args)
			if err != nil || !start {
				return err
			}
			// Give the Core preflight the same project flags as Atlas.
			// The original arguments still go to the launcher unchanged.
			if _, err := selectProductRoot(cmd, selection); err != nil {
				return err
			}
			projectPin, err = facet.ReadCorePin(ResolveRoot(cmd))
			if err != nil {
				return err
			}
			return cmd.Root().PersistentPreRunE(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return launcher.WithProjectPin(projectPin).Run(cmd.Context(), args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		fmt.Fprint(cmd.OutOrStdout(), atlaslauncher.Help)
	})
	return markPinExempt(cmd, pinExemptSelf)
}
