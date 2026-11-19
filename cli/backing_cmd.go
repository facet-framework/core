package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newBackingCmd() *cobra.Command {
	var asJSON bool
	var registry string

	cmd := &cobra.Command{
		Use:   "backing",
		Short: "List the declared behaviour that is deliberately not backed by data yet",
		Long: `Prints every scenario whose 'backing' says its data does not exist yet,
with the reason and what would supply it.

This is the question the field was added to make answerable. Before it, the
state lived in prose — a marker in a test's notes, a null on the wire, a
paragraph in a pull request — so "which declared behaviour is unbacked, and
what would unblock it?" could only be answered by reading the pull requests.

A scenario that declares nothing is backed, and is not listed. Nothing here
fails a run: a declared gap is an honest statement about the roadmap, not a
defect.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := configForCommand(cmd)
			if err != nil {
				return err
			}

			registries := cfg.All()
			if registry != "" {
				reg, err := cfg.Select(registry)
				if err != nil {
					return err
				}
				registries = []facet.Registry{reg}
			}

			var all []facet.Backing
			for _, reg := range registries {
				records, err := reg.LoadFeatures()
				if err != nil {
					return err
				}
				for _, b := range facet.UnbackedScenarios(records) {
					b.Path = reg.Rel(b.Path)
					all = append(all, b)
				}
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if all == nil {
					all = []facet.Backing{}
				}
				return enc.Encode(all)
			}
			printBacking(all)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the list as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

func printBacking(entries []facet.Backing) {
	if len(entries) == 0 {
		fmt.Println("No scenario declares itself unbacked.")
		return
	}
	features := map[string]bool{}
	for _, b := range entries {
		features[b.Feature] = true
		where := b.Scenario
		if b.Variant != "" {
			where += " (" + b.Variant + " only)"
		}
		fmt.Printf("%s — %s\n", b.Feature, where)
		// Reasons are usually written as YAML block scalars, so they arrive with
		// newlines. Collapsed for the list; the JSON output keeps them intact.
		fmt.Printf("    %-13s %s\n", "why:", collapse(b.Reason))
		fmt.Printf("    %-13s %s\n", "unblocked by:", collapse(b.UnblockedBy))
	}
	fmt.Printf("\n%d unbacked scenario(s) across %d feature(s)\n", len(entries), len(features))
}

// collapse folds a block scalar onto one line for list output.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
