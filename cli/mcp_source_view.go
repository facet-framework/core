// @facet: registry/layout/source-views
// @facet-role: related
package cli

import (
	"context"
	"fmt"
	"github.com/facet-framework/core/corpus"
	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sourceViewInput struct {
	Registry string `json:"registry,omitempty"`
}
type sourceViewOutput struct {
	Configured bool                       `json:"configured"`
	View       *corpus.SourceViewSnapshot `json:"view,omitempty"`
}

func describeSourceView(set facet.RegistrySet, in sourceViewInput) (sourceViewOutput, error) {
	reg := set.Primary()
	if in.Registry != "" {
		found := false
		for _, candidate := range set {
			if candidate.Name == in.Registry {
				reg = candidate
				found = true
				break
			}
		}
		if !found {
			return sourceViewOutput{}, fmt.Errorf("unknown registry %q", in.Registry)
		}
	}
	view, err := corpus.DescribeSourceView(reg)
	return sourceViewOutput{Configured: view != nil, View: view}, err
}
func addSourceViewTool(server *mcp.Server, set facet.RegistrySet) {
	mcp.AddTool(server, &mcp.Tool{Name: "source_view_get", Description: "Describe the selected implementation view and independent registry/source revisions and content identities without exposing local checkout paths."}, func(_ context.Context, _ *mcp.CallToolRequest, in sourceViewInput) (*mcp.CallToolResult, sourceViewOutput, error) {
		out, err := describeSourceView(set, in)
		return nil, out, err
	})
}
