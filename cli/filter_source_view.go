// @facet: registry/layout/source-views
// @facet-role: related
package cli

import (
	"fmt"
	"strings"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/spf13/cobra"
)

func filterSelectedSource(cmd *cobra.Command) (string, *sourceview.Source, error) {
	repository, _ := cmd.Flags().GetString("repository")
	reg := registryForCommand(cmd)
	if reg.SourceView == nil {
		if repository != "" {
			return "", nil, fmt.Errorf("--repository requires source_views")
		}
		return ResolveRoot(cmd), nil, nil
	}
	src, e := reg.SourceView.Source(repository)
	if e != nil {
		return "", nil, e
	}
	if e = src.ValidateWorkingTree(); e != nil {
		return "", nil, e
	}
	return src.Root, &src, nil
}
func privateRepositoryID(root string) string {
	return strings.TrimSpace(gitOutput(root, "config", "--get", "facet.privateRepository"))
}

func filterRuntimeSource(cmd *cobra.Command) (string, *sourceview.Source, error) {
	repository, _ := cmd.Flags().GetString("repository")
	if repository != "" || cmd.Flags().Changed("source-view") {
		return filterSelectedSource(cmd)
	}
	root, e := filterRoot()
	return root, nil, e
}
