package cli

import (
	"fmt"
	"os"
	"slices"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newSpecUpdateCmd() *cobra.Command {
	var only []string
	cmd := &cobra.Command{
		Use:   "update [source...]",
		Short: "Update the specification lock from the configured branches",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			cfg, err := facet.LoadConfigUnresolved(root)
			if err != nil {
				return err
			}
			sources, err := selectSpecSources(cfg.SpecSources, args)
			if err != nil {
				return err
			}
			overrides, err := sourceview.LoadLocalSpecSources(root)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for id, checkout := range overrides {
				if checkout == "" {
					return fmt.Errorf("spec source %q has an empty local checkout path", id)
				}
			}
			changes, err := spec.Update(cmd.Context(), root, sources, overrides, only)
			if err != nil {
				return err
			}
			for _, change := range changes {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s:%s [%s]\n", change.Kind, change.Source, change.Address, change.Binding)
			}
			if len(changes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no scenario pins changed")
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&only, "only", nil, "update only this <spec-id>#<case> address (repeatable)")
	return cmd
}

func selectSpecSources(sources []spec.Source, args []string) ([]spec.Source, error) {
	if len(args) > 0 {
		for _, id := range args {
			if !slices.ContainsFunc(sources, func(source spec.Source) bool { return source.ID == id }) {
				return nil, fmt.Errorf("spec source %q is not declared", id)
			}
		}
		sources = slices.DeleteFunc(slices.Clone(sources), func(source spec.Source) bool {
			return !slices.Contains(args, source.ID)
		})
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no spec sources are declared")
	}
	return sources, nil
}

func newSpecCheckLockCmd() *cobra.Command {
	return &cobra.Command{
		Use: "check-lock", Short: "Check record references against the specification lock",
		Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := facet.LintSpecConformance(ResolveRoot(cmd))
			if err != nil {
				return err
			}
			return emitLintReport(report, false)
		},
	}
}
