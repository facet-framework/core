// Package cli builds the FACET command surface and links no application of
// its own. An application that embeds this command tree selects optional
// commands, reader assets and the version.
//
// @facet: registry/integrity/record-validation
// @facet-role: primary
package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/facet-framework/core/registry"
	"github.com/facet-framework/core/release"
	"github.com/spf13/cobra"
)

// errSilent fails a command without printing anything further, for a command
// that has already reported in its own words.
var errSilent = errors.New("")

// Options selects the version and the optional additions an embedding
// application makes to a command tree.
// Nil ReaderAssets and no extra Commands produce the independent Core CLI.
type Options struct {
	// Version is the complete Core build stamp. Empty derives a development identity;
	// release stamps retain their SemVer prerelease and build metadata.
	Version string
	// Identity overrides Version when an application supplies build metadata.
	Identity *release.Identity
	// ReaderAssets selects the application's embedded reader. Nil produces
	// the headless Core setup report without linking a reader or its assets.
	ReaderAssets fs.FS
	// Commands are fresh application-owned command instances, such as a
	// product command `ui`. A command becomes part of this tree and must not
	// be reused elsewhere.
	Commands []*cobra.Command
}

// Run executes a command tree and preserves the CLI error and exit-code contract.
func Run(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		var silent interface{ Silent() bool }
		if err != errSilent && !(errors.As(err, &silent) && silent.Silent()) {
			fmt.Fprintln(os.Stderr, "facet:", err)
		}
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

// NewCommand builds the Core CLI, with optional application commands and reader assets.
func NewCommand(opts Options) *cobra.Command {
	identity := release.Current(opts.Version)
	if opts.Identity != nil {
		identity = *opts.Identity
	}
	version := identity.Version
	root := &cobra.Command{
		Use:           "facet",
		Short:         "Keep FACET records and the code they describe from drifting apart",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.PersistentFlags().String("root", "", "repository root (default: git toplevel, else cwd)")
	root.PersistentFlags().String("source-view", "", "select a declared implementation view (default: FACET_SOURCE_VIEW or configured default)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		// The project's Core pin is checked before anything else, so a
		// mismatched binary does no work at all (see corepin.go).
		if err := enforceCorePin(cmd, version); err != nil {
			return err
		}

		if cmd.Parent() != nil && cmd.Parent().Name() == "filter" && (cmd.Name() == "clean" || cmd.Name() == "smudge") && !cmd.Flags().Changed("source-view") {
			repository, _ := cmd.Flags().GetString("repository")
			if repository == "" {
				return nil
			}
		}
		// Staged lint reads both configurations from Git, never from unstaged
		// files. Its own resolver validates the invocation selection and bindings.
		if cmd.Name() == "lint" {
			staged, _ := cmd.Flags().GetBool("staged")
			if staged {
				return nil
			}
		}
		cfg, err := configForCommand(cmd)
		if err != nil {
			if sourceViewFlag(cmd) != "" || sourceview.IsError(err) {
				return err
			}
			return nil // Preserve legacy commands' own handling of ordinary config errors.
		}
		for _, reg := range cfg.All() {
			if err := reg.ValidateSourceView(); err != nil {
				return err
			}
		}
		cmd.SetContext(context.WithValue(cmd.Context(), commandConfigKey{}, cfg))
		return nil
	}
	root.AddCommand(newADRCmd(), newTestCmd(), newLintCmd(), newDocsCmd(), newHookCmd(), newMCPCmd(version), newServeCmd(), newLSPCmd(version), newRegistriesCmd(), newBackingCmd(), newDeviationsCmd(), newDefectsCmd(), newIndexCmd(), newDriftCmd(writeProductDriftReport), newRAGCmd(), newCalibrateCmd(), newSplitCmd(), newFilterCmd(), newErrorsCmd(), newPluginCmd(), newConventionsCmd(), newSetupCmd(opts.ReaderAssets, version), newSnapshotCmd(version))
	root.AddCommand(newVersionCmd(identity), newInitCmd(identity))
	root.AddCommand(opts.Commands...)
	registerProductCommands(root, productStore())
	registerMissingProductCommands(root)
	return root
}

// @facet: registry/layout/source-views
// @facet-role: related
type commandConfigKey struct{}

func sourceViewFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("source-view")
	return value
}

// One frozen resolution is shared by all consumers in a CLI invocation. No
// process-global environment mutation is needed, including embedded commands.
func configForCommand(cmd *cobra.Command) (*facet.Config, error) {
	if ctx := cmd.Context(); ctx != nil {
		if cfg, ok := ctx.Value(commandConfigKey{}).(*facet.Config); ok {
			return cfg, nil
		}
	}
	return facet.LoadConfigWithSourceView(ResolveRoot(cmd), sourceViewFlag(cmd))
}

func allRegistriesForCommand(cmd *cobra.Command) ([]facet.Registry, error) {
	cfg, err := configForCommand(cmd)
	if err != nil {
		return nil, err
	}
	return cfg.All(), nil
}

func registrySetForCommand(cmd *cobra.Command) facet.RegistrySet {
	regs, err := allRegistriesForCommand(cmd)
	if err == nil {
		return facet.RegistrySet(regs)
	}
	reg := facet.Open(ResolveRoot(cmd))
	if sourceview.IsError(err) || sourceViewFlag(cmd) != "" {
		reg.SourceView = sourceview.Failed(sourceViewFlag(cmd), err)
	}
	return facet.RegistrySet{reg}
}

func registryForCommand(cmd *cobra.Command) facet.Registry {
	return registrySetForCommand(cmd).Primary()
}

// ResolveRoot finds the repository root the same way the shell tooling does.
func ResolveRoot(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("root"); v != "" {
		abs, err := filepath.Abs(v)
		if err == nil {
			return abs
		}
		return v
	}
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// ConfigForCommand shares the invocation source view with application commands.
func ConfigForCommand(cmd *cobra.Command) (*facet.Config, error) { return configForCommand(cmd) }

// RegistrySetForCommand returns the same frozen source selection as Core commands.
func RegistrySetForCommand(cmd *cobra.Command) facet.RegistrySet { return registrySetForCommand(cmd) }
