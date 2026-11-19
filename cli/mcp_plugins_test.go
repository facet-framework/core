package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/facet-framework/core/registry"
)

// installPlugin writes a plugin directory the broker can read.
func installPlugin(t *testing.T, id string, tools, aiTools, grantedAI []string) string {
	t.Helper()
	dir := t.TempDir()
	writePluginAt(t, dir, id, tools, aiTools, grantedAI, `{"type":"object"}`)
	return dir
}

// installPluginRawSchema writes one plugin whose input schema is whatever the
// caller says, including things no schema may be.
func installPluginRawSchema(t *testing.T, id, tool, schema string) string {
	t.Helper()
	dir := t.TempDir()
	writePluginAt(t, dir, id, []string{tool}, []string{tool}, []string{tool}, schema)
	return dir
}

// writePlugin adds one granted plugin to an existing directory, so two can
// share it and collide.
func writePlugin(t *testing.T, dir, id, tool string) {
	t.Helper()
	writePluginAt(t, dir, id, []string{tool}, []string{tool}, []string{tool}, `{"type":"object"}`)
}

func writePluginAt(t *testing.T, dir, id string, tools, aiTools, grantedAI []string, schema string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	offered := make([]json.RawMessage, 0, len(aiTools))
	for _, name := range aiTools {
		entry, err := json.Marshal(map[string]any{
			"tool":        name,
			"description": "does " + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		// The schema is spliced in as raw text so a case can write something
		// json.Marshal would never produce from a Go value.
		withSchema := string(entry[:len(entry)-1]) + `,"input":` + schema + `}`
		offered = append(offered, json.RawMessage(withSchema))
	}
	manifest, err := json.Marshal(map[string]any{
		"id": id, "tools": tools, "ai_tools": offered,
		"sidecar": map[string]any{"bin": "helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	grant, err := json.Marshal(map[string]any{"tools": tools, "ai_tools": grantedAI})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, "grants.json"), grant, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The MCP server registers exactly the tools the user granted the agent.
func TestTheMCPServerRegistersGrantedPluginTools(t *testing.T) {
	dir := installPlugin(t, "wiki",
		[]string{"repo.state", "confluence.pull"},
		[]string{"repo.state", "confluence.pull"},
		[]string{"confluence.pull"},
	)
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)

	if n := addPluginAgentTools(server, facet.NewPluginBroker(dir)); n != 1 {
		t.Fatalf("registered %d tool(s), want 1", n)
	}

	want := facet.AgentToolName("wiki", "confluence.pull")
	unwanted := facet.AgentToolName("wiki", "repo.state")
	names := registeredToolNames(t, server)
	if !names[want] {
		t.Errorf("the granted tool %q was not registered; registered: %v", want, keys(names))
	}
	if names[unwanted] {
		t.Errorf("%q was registered although the user granted it only to the plugin", unwanted)
	}
}

// The advertised name is addressable as an MCP tool. UI argv agreement is checked separately.
func TestTheRegisteredNameIsAddressableAsAnMCPTool(t *testing.T) {
	dir := installPlugin(t, "wiki",
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
	)
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)
	addPluginAgentTools(server, facet.NewPluginBroker(dir))

	registered := keys(registeredToolNames(t, server))
	if len(registered) != 1 {
		t.Fatalf("registered %v, want exactly one tool", registered)
	}
	for _, r := range registered[0] {
		ok := r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			t.Fatalf("registered %q, which carries %q — not addressable as a tool name",
				registered[0], string(r))
		}
	}
	if !strings.HasPrefix(registered[0], "plugin_") {
		t.Errorf("registered %q, want the plugin_ prefix that keeps these apart "+
			"from the host's own tools", registered[0])
	}
}

// A manifest must not be able to take the process down.
func TestABadInputSchemaSkipsTheToolInsteadOfKillingTheProcess(t *testing.T) {
	for _, bad := range []string{`null`, `5`, `"hello"`, `{"type":"array"}`, `{"type":"banana"}`, `[]`} {
		t.Run(bad, func(t *testing.T) {
			dir := installPluginRawSchema(t, "wiki", "confluence.pull", bad)
			server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)

			n := addPluginAgentTools(server, facet.NewPluginBroker(dir))

			if n != 0 {
				t.Fatalf("registered %d tool(s) for schema %s, want 0", n, bad)
			}
			if got := len(registeredToolNames(t, server)); got != 0 {
				t.Fatalf("%d tool(s) with schema %s reached the server", got, bad)
			}
		})
	}
	// The precondition: a good schema through the same helper does register,
	// so the cases above are about the schema and not a broken fixture.
	dir := installPluginRawSchema(t, "wiki", "confluence.pull", `{"type":"object"}`)
	if n := addPluginAgentTools(mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil),
		facet.NewPluginBroker(dir)); n != 1 {
		t.Fatalf("the control fixture registered %d tool(s), want 1", n)
	}
}

// Two tools that fold onto one agent name are both dropped.
func TestCollidingAgentNamesAreBothDropped(t *testing.T) {
	// The precondition, and what makes this a collision rather than two
	// unusable plugins: both names really are the same string.
	if facet.AgentToolName("a", "b.c") != facet.AgentToolName("a-b", "c") {
		t.Fatal("the fixture does not collide; this case would prove nothing")
	}
	dir := t.TempDir()
	writePlugin(t, dir, "a", "b.c")
	writePlugin(t, dir, "a-b", "c")
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)

	if n := addPluginAgentTools(server, facet.NewPluginBroker(dir)); n != 0 {
		t.Fatalf("registered %d tool(s) for a colliding pair, want 0", n)
	}
	if got := len(registeredToolNames(t, server)); got != 0 {
		t.Fatalf("the server advertises %d tool(s) from a colliding pair", got)
	}
	// And each alone is offered, so neither plugin is broken by itself.
	solo := t.TempDir()
	writePlugin(t, solo, "a", "b.c")
	if n := addPluginAgentTools(mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil),
		facet.NewPluginBroker(solo)); n != 1 {
		t.Fatalf("the single-plugin control registered %d tool(s), want 1", n)
	}
}

// A manifest cannot write unbounded text into the model's context.
func TestALongToolDescriptionIsClipped(t *testing.T) {
	long := strings.Repeat("A", maxToolDescription*3)
	dir := t.TempDir()
	writePluginAt(t, dir, "wiki", []string{"confluence.pull"},
		[]string{"confluence.pull"}, []string{"confluence.pull"}, `{"type":"object"}`)
	// Rewrite the manifest with the long description in place.
	manifest := `{"id":"wiki","tools":["confluence.pull"],"ai_tools":[{"tool":"confluence.pull",` +
		`"description":"` + long + `","input":{"type":"object"}}],"sidecar":{"bin":"helper"}}`
	if err := os.WriteFile(filepath.Join(dir, "wiki", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)
	addPluginAgentTools(server, facet.NewPluginBroker(dir))

	got := describedTools(t, server)
	if len(got) != 1 {
		t.Fatalf("registered %d tool(s), want 1", len(got))
	}
	for _, description := range got {
		if len(description) > maxToolDescription+64 {
			t.Errorf("the description is %d bytes; a manifest may not write "+
				"unbounded text into the model's context", len(description))
		}
	}
}

// The handler really goes through the broker.
func TestACallThroughTheToolReachesTheBroker(t *testing.T) {
	dir := installPlugin(t, "wiki",
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
	)
	// The manifest names a binary that is not there, so the call gets as far
	// as the broker and fails in it — which is exactly what this asserts.
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)
	broker := facet.NewPluginBroker(dir)
	defer broker.StopAll()
	addPluginAgentTools(server, broker)

	result := callTool(t, server, facet.AgentToolName("wiki", "confluence.pull"),
		json.RawMessage(`{"id":"x"}`))

	if !result.IsError {
		t.Fatalf("a call that cannot reach a binary was reported as success: %s", firstText(result))
	}
	text := firstText(result)
	if text == "" {
		t.Fatal("the failure carried no reason the model could read")
	}
	// The broker's own words. A canned reply in the handler would not say
	// this, and neither would the transport.
	want := "plugin: wiki brings no binary for " + runtime.GOOS + "-" + runtime.GOARCH
	if text != want {
		t.Errorf("broker answer = %q, want %q", text, want)
	}
}

// A host with no plugin directory gets a server with no plugin tools.
func TestNoPluginDirectoryRegistersNothing(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)

	if n := addPluginAgentTools(server, facet.NewPluginBroker(filepath.Join(t.TempDir(), "absent"))); n != 0 {
		t.Fatalf("registered %d tool(s) from a directory that is not there", n)
	}
	if n := addPluginAgentTools(server, nil); n != 0 {
		t.Fatalf("registered %d tool(s) from a nil broker", n)
	}
}

// The command path wires the plugins in.
func TestTheServerTheCommandBuildsCarriesPluginTools(t *testing.T) {
	dir := installPlugin(t, "wiki",
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
	)

	server, stop := newMCPServerWithPlugins(facet.OpenSet(t.TempDir()), dir)
	defer stop()

	names := registeredToolNames(t, server)
	want := facet.AgentToolName("wiki", "confluence.pull")
	if !names[want] {
		t.Errorf("the server the command builds does not carry %q; it carries %v", want, keys(names))
	}
	// The host's own tools are still there: plugins are an addition.
	if !names["feature_find"] {
		t.Error("the server the command builds lost its own tools")
	}
}

// And without a directory it is exactly the server it was before.
func TestTheServerTheCommandBuildsWithoutPluginsIsUnchanged(t *testing.T) {
	server, stop := newMCPServerWithPlugins(facet.OpenSet(t.TempDir()), "")
	defer stop()

	for name := range registeredToolNames(t, server) {
		if strings.HasPrefix(name, "plugin_") {
			t.Errorf("a host with no plugin directory was given %q", name)
		}
	}
}

// session opens a client against the server and returns it.
func session(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("the server did not connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("the client did not connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

// registeredToolNames asks the server what it advertises, as a client.
func registeredToolNames(t *testing.T, server *mcp.Server) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for name := range describedTools(t, server) {
		out[name] = true
	}
	return out
}

func describedTools(t *testing.T, server *mcp.Server) map[string]string {
	t.Helper()
	list, err := session(t, server).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	out := map[string]string{}
	for _, tool := range list.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

func callTool(t *testing.T, server *mcp.Server, name string, args json.RawMessage) *mcp.CallToolResult {
	t.Helper()
	result, err := session(t, server).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("tools/call failed at the transport: %v", err)
	}
	return result
}

func firstText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAToolOfferedToTheAgentButNotDeclaredIsNotRegistered(t *testing.T) {
	dir := installPlugin(t, "wiki", []string{"confluence.pull"}, []string{"confluence.delete"}, []string{"confluence.delete"})
	server := mcp.NewServer(&mcp.Implementation{Name: "facet"}, nil)
	if got := addPluginAgentTools(server, facet.NewPluginBroker(dir)); got != 0 {
		t.Fatalf("registered undeclared tools: %d", got)
	}
	if got := registeredToolNames(t, server); len(got) != 0 {
		t.Fatalf("undeclared tool reached MCP: %v", got)
	}
}
