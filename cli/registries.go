// @facet: registry/layout/multi-registry
// @facet-role: related
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// coverageRow is one feature id and its state in each registry.
type coverageRow struct {
	ID    string            `json:"id"`
	State map[string]string `json:"state"` // registry -> lifecycle, or "" when absent
}

type coverageOutput struct {
	Registries []string      `json:"registries"`
	Rows       []coverageRow `json:"rows"`
	Shared     int           `json:"shared"`
	Total      int           `json:"total"`
}

func newRegistriesCmd() *cobra.Command {
	var asJSON bool
	var onlyGaps bool

	cmd := &cobra.Command{
		Use:   "registries",
		Short: "Show which features each registry documents",
		Long: `Prints the feature id × registry matrix.

Ids are global: the same id in two registries is the same feature, documented
independently by each product. That makes coverage comparable — this is where
you see that one registry has a feature the other has not, and what lifecycle
each is at.

A project declaring no registries has one column and nothing to compare, so the
command says so rather than printing a matrix of one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			set := registrySetForCommand(cmd)
			out := buildCoverage(set, onlyGaps)

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			if len(out.Registries) < 2 {
				fmt.Printf("This project declares no registries — %d feature record(s), nothing to compare.\n", out.Total)
				fmt.Printf("See %s and docs/REGISTRIES-GUIDE.md to declare some.\n", facet.ConfigName)
				return nil
			}
			printCoverage(out, onlyGaps)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the matrix as JSON")
	cmd.Flags().BoolVar(&onlyGaps, "gaps", false, "show only ids that some registry is missing")
	return cmd
}

func buildCoverage(set facet.RegistrySet, onlyGaps bool) coverageOutput {
	out := coverageOutput{Registries: []string{}, Rows: []coverageRow{}}
	byID := map[string]map[string]string{}

	for _, reg := range set {
		name := reg.Name
		if name == "" {
			name = facet.DefaultRegistryName
		}
		out.Registries = append(out.Registries, name)

		records, err := reg.LoadFeatures()
		if err != nil {
			continue
		}
		for _, rec := range records {
			if byID[rec.ID] == nil {
				byID[rec.ID] = map[string]string{}
			}
			byID[rec.ID][name] = lifecycleLabel(rec)
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		state := byID[id]
		out.Total++
		if len(state) > 1 {
			out.Shared++
		}
		if onlyGaps && len(state) == len(out.Registries) {
			continue
		}
		out.Rows = append(out.Rows, coverageRow{ID: id, State: state})
	}
	return out
}

// lifecycleLabel condenses a record's state to one cell. A record carrying
// per-variant lifecycles has no single value, so it reports the variants rather
// than inventing an aggregate — the exact thing §2.4 removed.
func lifecycleLabel(rec facet.Record) string {
	variants := rec.Variants()
	if variants == nil {
		if rec.Lifecycle == "" {
			return "?"
		}
		return rec.Lifecycle
	}
	var live []string
	for name, state := range variants {
		if state.Lifecycle == "active" {
			live = append(live, name)
		}
	}
	sort.Strings(live)
	if len(live) == 0 {
		return "planned(" + fmt.Sprint(len(variants)) + " variants)"
	}
	return "active:" + strings.Join(live, "+")
}

func printCoverage(out coverageOutput, onlyGaps bool) {
	width := len("FEATURE")
	for _, row := range out.Rows {
		if len(row.ID) > width {
			width = len(row.ID)
		}
	}
	colWidths := make([]int, len(out.Registries))
	for i, name := range out.Registries {
		colWidths[i] = len(name)
		for _, row := range out.Rows {
			if v := len(row.State[name]); v > colWidths[i] {
				colWidths[i] = v
			}
		}
	}

	fmt.Printf("%-*s", width+2, "FEATURE")
	for i, name := range out.Registries {
		fmt.Printf("%-*s", colWidths[i]+2, name)
	}
	fmt.Println()

	for _, row := range out.Rows {
		fmt.Printf("%-*s", width+2, row.ID)
		for i, name := range out.Registries {
			cell := row.State[name]
			if cell == "" {
				cell = "—"
			}
			fmt.Printf("%-*s", colWidths[i]+2, cell)
		}
		fmt.Println()
	}

	fmt.Printf("\n%d feature(s) across %d registries — %d documented by more than one.\n",
		out.Total, len(out.Registries), out.Shared)
	if !onlyGaps && out.Shared < out.Total {
		fmt.Println("Run with --gaps for only the ids some registry is missing.")
	}
	if out.Shared > 0 {
		fmt.Println("\nA shared id means the same feature, documented independently. If two rows")
		fmt.Println("here are not the same feature, one of them should be renamed.")
	}
}
