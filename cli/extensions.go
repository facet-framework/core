// @facet: navigation/interfaces/mcp-server
// @facet-role: related
package cli

import (
	"slices"
	"sync"

	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// MCPToolset is the set of MCP tools a linked product adds to 'facet mcp'.
type MCPToolset struct {
	// Add registers the tools on a server for the given registries.
	Add func(server *mcp.Server, set facet.RegistrySet)
	// Help is a paragraph appended to 'facet mcp --help'. Empty means none.
	Help string
}

var mcpToolsets struct {
	sync.Mutex
	sets []MCPToolset
}

// RegisterMCPTools adds a product's tools to every MCP server this build
// starts. Builds register from init, so the tools are in place before the
// command tree is built. It panics on a toolset without Add.
func RegisterMCPTools(set MCPToolset) {
	if set.Add == nil {
		panic("cli: RegisterMCPTools needs an Add function")
	}
	mcpToolsets.Lock()
	defer mcpToolsets.Unlock()
	mcpToolsets.sets = append(mcpToolsets.sets, set)
}

func registeredMCPToolsets() []MCPToolset {
	mcpToolsets.Lock()
	defer mcpToolsets.Unlock()
	return slices.Clone(mcpToolsets.sets)
}

// addRegisteredMCPTools adds every registered toolset, in registration order.
func addRegisteredMCPTools(server *mcp.Server, set facet.RegistrySet) {
	for _, toolset := range registeredMCPToolsets() {
		toolset.Add(server, set)
	}
}

// mcpToolsetHelp collects the help paragraphs of the registered toolsets.
func mcpToolsetHelp() string {
	text := ""
	for _, toolset := range registeredMCPToolsets() {
		if toolset.Help != "" {
			text += "\n\n" + toolset.Help
		}
	}
	return text
}

// AllRegistriesForCommand is every registry of this invocation's configuration,
// for a command a build adds to the tree.
func AllRegistriesForCommand(cmd *cobra.Command) ([]facet.Registry, error) {
	return allRegistriesForCommand(cmd)
}
