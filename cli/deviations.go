package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newDeviationsCmd lists the behaviour the registry documents as defective.
//
// @facet: registry/integrity/declared-deviations
// @facet-role: related
func newDeviationsCmd() *cobra.Command {
	var asJSON bool
	var registry string

	cmd := &cobra.Command{
		Use:   "deviations",
		Short: "List the documented behaviour that is a defect rather than a promise",
		Long: `Prints every scenario whose 'deviation' says the behaviour it describes is
not what anybody intended, with the reason, how it was confirmed, and where the
fix is tracked.

A scenario that declares nothing is a promise, and is not listed. Nothing here
fails a run: writing a defect down is the honest act the field exists for, and
a gate that punished it would teach authors back into the two bad options it
replaced — stating the defect as a contract, or leaving it out.

What is deliberately absent from every entry is the behaviour somebody thinks
the code ought to have. A deviation names only what the code does; where a fix
has been decided, 'tracked as' points at the record or the ticket that decides
it.`,
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

			var all []facet.Deviation
			for _, reg := range registries {
				records, err := reg.LoadFeatures()
				if err != nil {
					return err
				}
				for _, d := range facet.DeclaredDeviations(records) {
					d.Path = reg.Rel(d.Path)
					all = append(all, d)
				}
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if all == nil {
					all = []facet.Deviation{}
				}
				return enc.Encode(all)
			}
			printDeviations(cmd, all)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the list as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

func printDeviations(cmd *cobra.Command, entries []facet.Deviation) {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "No scenario declares itself a deviation.")
		return
	}
	features := map[string]bool{}
	for _, d := range entries {
		features[d.Feature] = true
		fmt.Fprintf(out, "%s — %s\n", d.Feature, d.Scenario)
		// Reasons are usually written as YAML block scalars, so they arrive
		// with newlines. Collapsed for the list; the JSON output keeps them.
		fmt.Fprintf(out, "    %-13s %s\n", "why:", collapse(d.Reason))
		// An unrecorded confirmation is printed as a dash rather than guessed
		// at or silently omitted: nobody said how this was established, and
		// that is a different thing from nobody having established it.
		fmt.Fprintf(out, "    %-13s %s\n", "confirmed:", dashIfEmpty(d.ConfirmedBy))
		fmt.Fprintf(out, "    %-13s %s\n", "tracked as:", dashIfEmpty(collapse(d.TrackedAs)))
	}
	fmt.Fprintf(out, "\n%d declared deviation(s) across %d feature(s)\n", len(entries), len(features))
}

// dashIfEmpty renders an unstated value as a dash. Printing nothing would read
// as a value that is empty; printing a guess would be worse than either.
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
