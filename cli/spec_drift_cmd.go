package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newSpecDriftCmd() *cobra.Command {
	var asJSON, diff, strict, cached bool
	cmd := &cobra.Command{
		Use:   "drift [source...]",
		Short: "Compare pinned specifications with their follow branches",
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
			report, err := facet.CompareSpecDrift(cmd.Context(), root, cfg.All(), sources, cached, diff)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(out).Encode(report); err != nil {
					return err
				}
			} else {
				for _, finding := range report.Findings {
					fmt.Fprintln(out, formatSpecDriftFinding(finding))
					if finding.Diff != "" {
						fmt.Fprintln(out, finding.Diff)
					}
				}
			}
			if (strict && len(report.Findings) > 0) || (os.Getenv("FACET_STRICT_DEPS") == "1" &&
				slices.ContainsFunc(report.Findings, func(f facet.SpecDriftFinding) bool { return f.State == spec.Unmeasured })) {
				return errSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit findings as JSON")
	cmd.Flags().BoolVar(&diff, "diff", false, "show differences in normalised steps or provenance text")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail when any finding is reported")
	cmd.Flags().BoolVar(&cached, "cached", false, "compare with the last fetched branch tips without fetching")
	return cmd
}

func formatSpecDriftFinding(f facet.SpecDriftFinding) string {
	address := f.Address
	if address == "" {
		address = f.Source
	}
	records := "none"
	if len(f.Records) > 0 {
		records = strings.Join(f.Records, ", ")
	}
	// Keep parser diagnostics for an unreadable source on the finding line.
	detail := strings.NewReplacer("\r", " ", "\n", " ").Replace(f.Detail)
	return fmt.Sprintf("%s %s [%s] (records: %s): %s", f.State, address, f.Binding, records, detail)
}
