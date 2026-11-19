// @facet: authoring/guardrails/adr-status-proposal
// @facet-role: related
package cli

import (
	"encoding/json"
	"fmt"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newADRCmd() *cobra.Command {
	command := &cobra.Command{Use: "adr", Short: "Inspect architecture decision proposals without writing files"}
	var target, reason, successor string
	var asJSON bool
	proposal := &cobra.Command{Use: "propose-status <path>", Short: "Print a read-only status diff for an existing Markdown file in the configured ADR directory", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := facet.ProposeADRStatus(ResolveRoot(cmd), args[0], target, reason, successor)
		if err != nil {
			return err
		}
		if asJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		}
		if result.Achieved {
			fmt.Fprintln(cmd.OutOrStdout(), "Target status already reached; no proposal.")
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Read-only proposal for %s; no files, branches or records changed.\nReason: %s\n", result.Path, result.Reason)
		if result.Successor != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Successor (proposal metadata only): %s\n", result.Successor)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Existing suffix metadata is preserved verbatim; review any dates before applying elsewhere.")
		fmt.Fprint(cmd.OutOrStdout(), result.Diff)
		return nil
	}}
	proposal.Flags().StringVar(&target, "status", "", "one of the four ADR statuses: proposed, accepted, superseded or rejected")
	proposal.Flags().StringVar(&reason, "reason", "", "reason for this proposal (required for a changed status)")
	proposal.Flags().StringVar(&successor, "superseded-by", "", "existing different ADR file for a superseded proposal; metadata only")
	proposal.Flags().BoolVar(&asJSON, "json", false, "emit the read-only proposal and diff as JSON")
	command.AddCommand(proposal)
	return command
}
