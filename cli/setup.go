package cli

import (
	"github.com/spf13/cobra"
	"io/fs"
)

// setupSubcommands is where every 'facet setup <step>' registers itself.
var setupSubcommands []func() *cobra.Command

func newSetupCmd(readerAssets fs.FS, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Measure and wire up a project's FACET setup",
		Long: `Checks and configuration for FACET. Each subcommand does one step of getting a
project from "FACET installed" to "FACET acting where it has to": it reports
what it would do or what it measured, and writes only when told to.`,
	}
	cmd.AddCommand(newSetupStatusCmd(readerAssets, version))
	for _, build := range setupSubcommands {
		cmd.AddCommand(build())
	}
	// Setup is how an installation is repaired, so a project pin this build
	// does not meet never stops it or any of its subcommands.
	return markPinExempt(cmd, pinExemptSubtree)
}
