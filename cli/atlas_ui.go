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
			pin, err := facet.ReadCorePin(ResolveRoot(cmd))
			if err != nil {
				return err
			}
			core := launcher.AtlasCoreVersion()
			if pin != nil && !pin.Satisfied(core) {
				return fmt.Errorf("this project requires FACET Core %s; facet ui starts FACET Atlas %s, which contains Core %s; facet ui opens this project once a FACET Atlas release contains Core %s",
					pin.Core, launcher.Version(), core, pin.Core)
			}
			return cmd.Root().PersistentPreRunE(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return launcher.Run(cmd.Context(), args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		fmt.Fprint(cmd.OutOrStdout(), atlaslauncher.Help)
	})
	return markPinExempt(cmd, pinExemptSelf)
}
