package facet_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/facet-framework/core/registry"
)

// agentPluginDir is pluginDir with an ai_tools section, so the agent's own
// grant is a separate answer from the plugin's.
func agentPluginDir(t *testing.T, tools, aiTools []string, grant map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	id := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id, "confluence-"+platform), src, 0o755); err != nil {
		t.Fatal(err)
	}
	offered := make([]map[string]any, 0, len(aiTools))
	for _, name := range aiTools {
		offered = append(offered, map[string]any{
			"tool":        name,
			"description": "does " + name,
			"input":       map[string]any{"type": "object"},
		})
	}
	writeJSON(t, filepath.Join(id, "plugin.json"), map[string]any{
		"id":       "wiki",
		"tools":    tools,
		"ai_tools": offered,
		"sidecar": map[string]any{
			"bin": "confluence",
			"env": []string{helperEnv, markerEnv},
			// Offered to the agent as well: the agent's sidecar is its own
			// process with its own environment, and a child without the
			// helper variable has no mode to run in.
			"ai_env": []string{helperEnv, markerEnv},
			"sha256": map[string]string{platform: selfSum(t)},
		},
	})
	if grant != nil {
		writeJSON(t, filepath.Join(id, "grants.json"), grant)
	}
	return dir
}

// A tool the user granted the plugin but not the agent never runs.
func TestAnAgentCallTheUserDidNotGrantNeverReachesTheBinary(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	dir := agentPluginDir(t,
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		map[string]any{
			// Granted to the plugin's own surface, withheld from the agent.
			// The ENVIRONMENT is granted to the agent so that a binary that
			// did run would have its mode and write the marker; the tool is
			// what is withheld.
			"tools":    []string{"confluence.pull"},
			"ai_tools": []string{},
			"env":      []string{helperEnv, markerEnv},
			"ai_env":   []string{helperEnv, markerEnv},
		})
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	_, err := b.CallForAgent(context.Background(), "wiki", "confluence.pull", nil)

	if !errors.Is(err, facet.ErrToolNotGranted) {
		t.Fatalf("an ungranted agent call was allowed: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the binary ran before the agent's grant was checked")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// The same fixture with the agent grant present does reach the binary.
func TestAnAgentCallTheUserGrantedReachesTheBinary(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	dir := agentPluginDir(t,
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		map[string]any{
			"tools":    []string{"confluence.pull"},
			"ai_tools": []string{"confluence.pull"},
			"env":      []string{helperEnv, markerEnv},
			"ai_env":   []string{helperEnv, markerEnv},
		})
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	reply, err := b.CallForAgent(context.Background(), "wiki", "confluence.pull", nil)

	if err != nil {
		t.Fatalf("a granted agent call was refused: %v", err)
	}
	if reply.Error != "" {
		t.Fatalf("the binary answered with an error: %s", reply.Error)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the binary did not run: %v", err)
	}
}

// A tool the manifest never offered the agent is refused even when granted.
func TestAToolTheManifestDoesNotOfferTheAgentIsRefused(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	dir := agentPluginDir(t,
		[]string{"confluence.pull"},
		nil, // offered to nobody
		map[string]any{
			"tools":    []string{"confluence.pull"},
			"ai_tools": []string{"confluence.pull"},
			"env":      []string{helperEnv, markerEnv},
		})
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	_, err := b.CallForAgent(context.Background(), "wiki", "confluence.pull", nil)

	if !errors.Is(err, facet.ErrToolNotDeclared) {
		t.Fatalf("a tool nobody offered the agent was allowed: %v", err)
	}
	// And it is not advertised either, so the model never sees it to try.
	if tools := b.AgentTools(); len(tools) != 0 {
		t.Fatalf("AgentTools offered %d tool(s) the manifest does not", len(tools))
	}
}

// The name the agent addresses a tool by is stable and safe to send.
func TestTheAgentToolNameFoldsWhatMCPCannotCarry(t *testing.T) {
	got := facet.AgentToolName("wiki", "confluence.pull")

	if got != "plugin_wiki_confluence_pull" {
		t.Errorf("AgentToolName = %q, want plugin_wiki_confluence_pull", got)
	}
	// The dot has to go: it is not safe in every client's idea of a tool
	// name, and this name is an address rather than something parsed back.
	if ref := facet.AgentToolRef("facet", "wiki", "confluence.pull"); ref != "mcp__facet__"+got {
		t.Errorf("AgentToolRef = %q, want mcp__facet__%s", ref, got)
	}
}
