package cli

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/facet-framework/core/registry"
)

// The names MCPToolNames reports are the ones a client of `facet mcp` receives.
func TestMCPToolNamesAreWhatTheServerAdvertises(t *testing.T) {
	root := t.TempDir()
	set := facet.OpenSet(root)

	got, err := MCPToolNames(context.Background(), set, "", "test")
	if err != nil {
		t.Fatal(err)
	}

	server, stop := newMCPServerWithPlugins(set, "", "test")
	defer stop()
	want := keys(registeredToolNames(t, server))
	sort.Strings(want)

	// Precondition: the server offers tools at all, so equality is not two
	// empty lists agreeing.
	if len(want) == 0 {
		t.Fatal("the server advertises no tools, so this compares nothing")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listed tools %v, server tools %v", got, want)
	}
	found := false
	for _, n := range got {
		if n == "feature_find" {
			found = true
		}
	}
	if !found {
		t.Fatalf("feature_find missing from %v", got)
	}
}

// A granted plugin tool is listed by MCPToolNames, as `facet mcp` registers it.
// Ignoring the plugin directory would make the listed names drift from the
// server's: the earlier tests pass "" and could not see it.
func TestMCPToolNamesIncludeGrantedPluginTools(t *testing.T) {
	dir := installPlugin(t, "wiki",
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
		[]string{"confluence.pull"},
	)
	got, err := MCPToolNames(context.Background(), facet.OpenSet(t.TempDir()), dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := facet.AgentToolName("wiki", "confluence.pull")
	for _, n := range got {
		if n == want {
			return
		}
	}
	t.Fatalf("granted plugin tool %q missing from %v", want, got)
}
