package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// calibrateReport is what 'facet calibrate --json' prints: one entry per
// registry, so a monorepo's products are comparable side by side rather than
// averaged into a shape neither of them has.
//
// @facet: registry/layout/granularity-calibration
// @facet-role: related
type calibrateReport struct {
	Registries []facet.Calibration `json:"registries"`
}

// newCalibrateCmd reports the shape of a registry rather than the health of any
// record in it.
//
// @facet: registry/layout/granularity-calibration
// @facet-role: primary
func newCalibrateCmd() *cobra.Command {
	var asJSON bool
	var registry string

	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Report the shape of the registry: how finely the product has been documented",
		Long: `Measures granularity, which no other check can see.

Every lint check judges one record. A registry of three thousand records with
one scenario each and a registry of thirty records with forty scenarios each
both pass them, and they are the same product documented two levels apart —
one at the granularity of a behaviour, one at the granularity of a product
area. Both are recoverable by moving one id segment, and neither is visible
without looking at the list as a whole.

The bands are guidance (FACET 11.4), not rules. Nothing here fails a run: a
project whose shape is genuinely unusual keeps its shape, and a check that
could fail would be answered by tuning the number rather than the records.

Works without a declared taxonomy, grouping by the id segments as they are —
the registries most in need of the answer are the ones that never declared
one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := configForCommand(cmd)
			if err != nil {
				return err
			}

			registries := cfg.All()
			if registry != "" {
				reg, selErr := cfg.Select(registry)
				if selErr != nil {
					return selErr
				}
				registries = []facet.Registry{reg}
			}

			report := calibrateReport{Registries: []facet.Calibration{}}
			for _, reg := range registries {
				scanOpts := facet.ScanOptionsFromEnv()
				if len(reg.Sources) > 0 {
					scanOpts = reg.ScanOptions()
				}
				cal, calErr := facet.Calibrate(reg, scanOpts)
				if calErr != nil {
					return calErr
				}
				report.Registries = append(report.Registries, cal)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			printCalibration(report)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the calibration as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "measure only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

func printCalibration(report calibrateReport) {
	for i, cal := range report.Registries {
		if i > 0 {
			fmt.Println()
		}
		name := cal.Registry
		if name == "" {
			name = "(default)"
		}
		fmt.Printf("registry: %-20s records %d   scenarios %d   anchored files %d\n",
			name, cal.Records, cal.Scenarios, cal.AnchoredFiles)
		fmt.Printf("          %-20s domains %d   capabilities %d   taxonomy %s\n",
			"", cal.Domains, cal.Capabilities, declared(cal.HasTaxonomy))
		// Three numbers, not a percentage. A quota hides whether the
		// denominator grew or the numerator fell, and the expectation being
		// watched is about one of the three specifically: that the declared
		// exceptions stay well above zero without becoming the ordinary case.
		k := cal.Knowledge
		fmt.Printf("          %-20s knowledge: %d answered   %d open   %d not applicable\n",
			"", k.Answered, k.Open, k.Inapplicable)
		fmt.Println()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, m := range cal.Metrics {
			if m.Samples == 0 {
				continue
			}
			flag := ""
			if m.Below > 0 || m.Above > 0 {
				flag = "!"
			}
			note := fmt.Sprintf("band %d-%d", m.Band.Low, m.Band.High)
			if m.Below > 0 {
				note += fmt.Sprintf(", %d below", m.Below)
			}
			if m.Above > 0 {
				note += fmt.Sprintf(", %d above", m.Above)
			}
			fmt.Fprintf(w, "  %s\tmedian %s\t%s\t%s\n", m.Name, trimFloat(m.Median), note, flag)
		}
		w.Flush()

		if len(cal.Readings) > 0 {
			fmt.Println()
			for j, r := range cal.Readings {
				label := "  reading:"
				if j > 0 {
					label = "          "
				}
				fmt.Printf("%s %s\n", label, wrapReading(r))
			}
		}
	}
}

func declared(ok bool) string {
	if ok {
		return "declared"
	}
	return "not declared"
}

func trimFloat(v float64) string {
	if v == float64(int(v)) {
		return fmt.Sprintf("%d", int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// wrapReading breaks a reading onto continuation lines aligned under the first,
// so a paragraph-length diagnosis stays readable in a terminal.
func wrapReading(s string) string {
	const width = 66
	const indent = "           "
	var out strings.Builder
	line := 0
	for i, word := range strings.Fields(s) {
		if line > 0 && line+1+len(word) > width {
			out.WriteString("\n" + indent)
			line = 0
		} else if i > 0 {
			out.WriteString(" ")
			line++
		}
		out.WriteString(word)
		line += len(word)
	}
	return out.String()
}
