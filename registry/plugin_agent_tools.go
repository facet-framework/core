package facet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// AgentTool is one plugin tool the agent may call through facet mcp.
type AgentTool struct {
	Plugin      string
	Tool        string
	Description string
	// Input is the JSON Schema the plugin declared, passed to the model as
	// written. Empty means the tool takes nothing.
	Input json.RawMessage
}

// AgentToolName is the name the agent calls a plugin tool by, without the MCP
// server prefix.
func AgentToolName(plugin, tool string) string {
	return "plugin_" + mcpSafe(plugin) + "_" + mcpSafe(tool)
}

// AgentToolRef is the full name Claude Code addresses the tool by.
func AgentToolRef(server, plugin, tool string) string {
	return "mcp__" + server + "__" + AgentToolName(plugin, tool)
}

// mcpSafe reduces an identifier to what an MCP tool name may carry.
func mcpSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// AgentTools lists every tool the user has granted to the agent, across
// every installed plugin.
func (b *PluginBroker) AgentTools() []AgentTool {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil
	}
	var out []AgentTool
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		m, err := b.Manifest(id)
		if err != nil {
			continue
		}
		g, err := b.Granted(id)
		if err != nil {
			continue
		}
		for _, t := range m.AITools {
			if !names(m.Tools, t.Tool) {
				// Declared for the agent but not as a tool at all. The
				// manifest contradicts itself; the broker would refuse the
				// call anyway, so do not advertise it.
				continue
			}
			// Both the ordinary and agent grants are required by AllowedForAgent.
			// Advertise only tools with both grants so listed tools can be called.
			if !names(g.Tools, t.Tool) || !names(g.AITools, t.Tool) {
				continue
			}
			if !validAgentSchema(t.Input) {
				// A schema the MCP server would reject. Skipping it here is
				// what keeps that rejection from taking the whole process
				// down with it, and keeps this list identical to what the
				// server will actually register.
				continue
			}
			out = append(out, AgentTool{
				Plugin:      id,
				Tool:        t.Tool,
				Description: t.Description,
				Input:       t.Input,
			})
		}
	}
	// Stable order, so two processes building the same list from the same
	// directory produce the same --tools string and the same registration
	// order. os.ReadDir sorts, but the inner loop follows manifest order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plugin != out[j].Plugin {
			return out[i].Plugin < out[j].Plugin
		}
		return out[i].Tool < out[j].Tool
	})
	return withoutCollisions(out)
}

// withoutCollisions drops every tool that shares its agent name with another.
func withoutCollisions(tools []AgentTool) []AgentTool {
	seen := map[string]int{}
	for _, t := range tools {
		seen[AgentToolName(t.Plugin, t.Tool)]++
	}
	out := make([]AgentTool, 0, len(tools))
	for _, t := range tools {
		if seen[AgentToolName(t.Plugin, t.Tool)] == 1 {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validAgentSchema says whether the MCP server will accept this input schema.
func validAgentSchema(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	kind, ok := obj["type"].(string)
	return ok && kind == "object"
}

// AllowedForAgent is Allowed plus the agent's own grant.
func (b *PluginBroker) AllowedForAgent(id, tool string) error {
	m, err := b.Manifest(id)
	if err != nil {
		return err
	}
	offered := false
	for _, t := range m.AITools {
		if t.Tool == tool {
			offered = true
			break
		}
	}
	if !offered {
		return fmt.Errorf("%w: %s is not offered to the agent", ErrToolNotDeclared, tool)
	}
	g, err := b.Granted(id)
	if err != nil {
		return err
	}
	if !names(g.AITools, tool) {
		return fmt.Errorf("%w: %s is not granted to the agent", ErrToolNotGranted, tool)
	}
	// And everything an ordinary call has to satisfy. The agent gets no
	// shortcut past the manifest's tool list or the ordinary grant.
	return b.Allowed(id, tool)
}

// CallForAgent brokers one call made by the agent.
func (b *PluginBroker) CallForAgent(ctx context.Context, id, tool string, args json.RawMessage) (SidecarReply, error) {
	if err := b.AllowedForAgent(id, tool); err != nil {
		return SidecarReply{}, err
	}
	side, err := b.sidecarFor(id, forAgent)
	if err != nil {
		return SidecarReply{}, err
	}
	return side.Call(ctx, SidecarCall{Tool: tool, Args: args})
}
