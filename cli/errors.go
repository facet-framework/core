package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newErrorsCmd exposes error codes extracted from record intent, scenarios and
// common pitfalls. The pattern is configurable; a separate hand-maintained code
// register would drift from the records that already declare the codes.
// @facet: registry/integrity/error-code-register
// @facet-role: related
func newErrorsCmd() *cobra.Command {
	var asJSON bool
	var registry string

	cmd := &cobra.Command{
		Use:   "errors",
		Short: "List identifiers found in scenario text, grouped and gap-checked by band",
		Long: `Extracts a stable identifier — an E1074-style banded error code by
default — from intent.summary, contract.scenarios[].statement, and
knowledge.common_pitfalls across every record, and reports each one's band,
which records mention it, and which numbers in that band nothing has claimed.

The pattern is a project convention, not a FACET concept: it defaults to
'E\d{4}' but is configurable via .facetrc.yaml's error_codes.pattern, so any
project with stable identifiers in its scenario text gets the same register.

The value is answering "what is the next free code in this band" without
grepping a service's source and hoping. 'facet lint's error-codes check
separately warns when the same code turns up in records from more than one
domain.

Those three fields are the whole scan. A code named anywhere else on a record
— a scenario id, a deviation's reason — is not seen, so the register is a
lower bound on what a project has assigned and 'next free' is a claim about
the scanned fields alone. Check a band before committing to a code from it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			cfg, err := facet.LoadConfig(root)
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

			var all []facet.Record
			for _, reg := range registries {
				records, err := reg.LoadFeatures()
				if err != nil {
					return err
				}
				all = append(all, records...)
			}

			register, err := facet.ExtractErrorCodes(all, cfg.ErrorCodePattern())
			if err != nil {
				return err
			}
			// Every registry in one project shares the same repository root
			// (only records/sources differ), so any one of them renders a
			// path relative to it the same way the others would.
			if len(registries) > 0 {
				relativizePaths(registries[0], &register)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(register)
			}
			printErrorCodes(register)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the register as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

func relativizePaths(reg facet.Registry, register *facet.ErrorCodeRegister) {
	for i := range register.Codes {
		for j := range register.Codes[i].Mentions {
			register.Codes[i].Mentions[j].Path = reg.Rel(register.Codes[i].Mentions[j].Path)
		}
	}
}

func printErrorCodes(r facet.ErrorCodeRegister) {
	if len(r.Codes) == 0 {
		fmt.Println("No identifiers found.")
		return
	}

	for _, b := range r.Bands {
		fmt.Printf("%s — %d code(s)", b.Band, len(b.Codes))
		if b.Next != "" {
			fmt.Printf(", next free: %s", b.Next)
		}
		fmt.Println()
		if len(b.Gaps) > 0 {
			fmt.Printf("    gaps: %s\n", strings.Join(b.Gaps, ", "))
		}
	}
	if len(r.Bands) > 0 {
		fmt.Println()
	}

	for _, ec := range r.Codes {
		fmt.Printf("%s — %s\n", ec.Code, strings.Join(ec.Features(), ", "))
	}
	fmt.Printf("\n%d identifier(s) found across %d band(s)\n", len(r.Codes), len(r.Bands))
}
