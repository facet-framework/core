package cli

import (
	"fmt"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newIndexCmd prints the record roster an agent is given.
//
// @facet: navigation/interfaces/record-roster
// @facet-role: related
func newIndexCmd() *cobra.Command {
	var (
		bare  bool
		bytes int
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Print the record index agents are given",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			regs, err := allRegistriesForCommand(cmd)
			if err != nil {
				return err
			}
			text := facet.RecordIndex(regs, facet.IndexOptions{
				Summaries: !bare,
				MaxBytes:  bytes,
			})
			if text == "" {
				// Not an error: a project with no records is a project at its
				// beginning, and reporting that as a failure would make 'facet
				// index' fail in every repository on its first day.
				fmt.Fprintln(cmd.OutOrStdout(), "No records to index.")
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	}
	cmd.Flags().BoolVar(&bare, "bare", false, "ids only, without the intent summaries")
	cmd.Flags().IntVar(&bytes, "max-bytes", 0,
		fmt.Sprintf("budget for the rendered index (default %d)", facet.DefaultIndexBytes))
	return cmd
}
