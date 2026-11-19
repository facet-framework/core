// Plugin tools, offered to any MCP client through the host's own MCP server,
// `facet mcp`.
package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPServerWithPlugins builds the server `facet mcp` serves, and the
// function that ends what it started.
func newMCPServerWithPlugins(set facet.RegistrySet, dir string, versions ...string) (*mcp.Server, func()) {
	server := buildMCPServer(set, versions...)
	if dir == "" {
		return server, func() {}
	}
	broker := facet.NewPluginBroker(dir)
	addPluginAgentTools(server, broker)
	return server, broker.StopAll
}

// addPluginAgentTools registers each granted plugin tool and returns how many.
func addPluginAgentTools(server *mcp.Server, broker *facet.PluginBroker) int {
	if broker == nil {
		return 0
	}
	tools := broker.AgentTools()
	for _, t := range tools {
		plugin, tool := t.Plugin, t.Tool
		schema := t.Input
		if len(schema) == 0 {
			// A tool that declared no schema still has to say it takes an
			// object, or a client has nothing to send.
			schema = json.RawMessage(`{"type":"object"}`)
		}
		server.AddTool(&mcp.Tool{
			Name: facet.AgentToolName(plugin, tool),
			// The plugin is named in the description because the model sees a
			// flat list of tool names and the user granted this per plugin.
			Description: fmt.Sprintf("%s (from the %q plugin)", clip(t.Description), plugin),
			InputSchema: schema,
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return callPluginAgentTool(ctx, broker, plugin, tool, req)
		})
	}
	return len(tools)
}

// callPluginAgentTool brokers one call the agent made.
func callPluginAgentTool(
	ctx context.Context,
	broker *facet.PluginBroker,
	plugin, tool string,
	req *mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	var args json.RawMessage
	if req != nil && req.Params != nil {
		args = req.Params.Arguments
	}
	reply, err := broker.CallForAgent(ctx, plugin, tool, args)
	if err != nil {
		return toolError(err.Error()), nil
	}
	if reply.Error != "" {
		return toolError(reply.Error), nil
	}
	text := string(reply.Result)
	if text == "" {
		text = "{}"
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

// maxToolDescription bounds what a manifest may write into the model's
// context.
const maxToolDescription = 500

func clip(s string) string {
	if len(s) <= maxToolDescription {
		return s
	}
	return s[:maxToolDescription] + "…"
}

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
	}
}
