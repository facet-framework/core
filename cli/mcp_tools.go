package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/facet-framework/core/registry"
)

// MCPToolNames asks the server `facet mcp` serves which tools it advertises,
// over a real in-memory MCP session, and returns their names in sorted order.
//
// @facet: navigation/interfaces/mcp-server
// @facet-role: related
func MCPToolNames(ctx context.Context, set facet.RegistrySet, pluginsDir, version string) ([]string, error) {
	server, stop := newMCPServerWithPlugins(set, pluginsDir, version)
	defer stop()

	clientT, serverT := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		return nil, fmt.Errorf("the MCP server did not connect: %w", err)
	}
	defer serverSession.Close()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "facet-tool-names"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		return nil, fmt.Errorf("the MCP client did not connect: %w", err)
	}
	defer clientSession.Close()

	var names []string
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("listing the MCP tools: %w", err)
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names, nil
}
