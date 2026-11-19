// @facet: navigation/documentation/guided-documentation
// @facet-role: related
package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/facet-framework/core/internal/docsite"
	"github.com/spf13/cobra"
)

func newDocsSiteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "site", Short: "Plan and build audience-guided standalone documentation"}
	inventory := &cobra.Command{Use: "inventory", Args: cobra.NoArgs, Short: "Collect all registries, evidence and Knowledge gaps", RunE: func(cmd *cobra.Command, args []string) error {
		inv, e := docsite.Collect(ResolveRoot(cmd))
		if e != nil {
			return e
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(inv)
	}}
	cmd.AddCommand(inventory)
	for _, action := range []string{"plan", "compose", "build", "check"} {
		action := action
		var config, output string
		child := &cobra.Command{Use: action, Args: cobra.NoArgs, Short: action + " documentation using a reviewed editorial JSON configuration", RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			configPath := config
			if !filepath.IsAbs(configPath) {
				configPath = filepath.Join(root, configPath)
			}
			outputPath := output
			if outputPath != "" && !filepath.IsAbs(outputPath) {
				outputPath = filepath.Join(root, outputPath)
			}
			c, e := docsite.ReadConfig(configPath)
			if e != nil {
				return e
			}
			switch action {
			case "compose":
				inv, e := docsite.Collect(root)
				if e != nil {
					return e
				}
				draft, e := docsite.Compose(inv, c)
				if e != nil {
					return e
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(draft)
			case "plan":
				inv, e := docsite.Collect(root)
				if e != nil {
					return e
				}
				p, e := docsite.MakePlan(inv, c)
				if e != nil {
					return e
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(p)
			case "build":
				if e = docsite.Build(root, outputPath, c); e != nil {
					return e
				}
			case "check":
				if e = docsite.Check(root, outputPath, c); e != nil {
					return e
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Documentation "+action+": "+output)
			return nil
		}}
		child.Flags().StringVar(&config, "config", "docs/site.json", "editorial configuration file")
		if action != "plan" && action != "compose" {
			child.Flags().StringVar(&output, "output", "docs-site", "standalone output directory (build requires a new path)")
		}
		cmd.AddCommand(child)
	}
	return cmd
}
