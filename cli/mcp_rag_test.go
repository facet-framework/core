// @facet-ignore-file

package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/facet-framework/core/rag"
	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// indexedProject builds a two-record registry with an index over it.
func indexedProject(t *testing.T, withIndex bool) facet.RegistrySet {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/dunning.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/dunning\nlifecycle: active\n"+
			"intent:\n  summary: Chases an unpaid invoice on a schedule, and stops when it is paid.\n")
	write(t, filepath.Join(root, "docs/facets/features/auth/login/password.yaml"),
		"schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n"+
			"intent:\n  summary: Sign in with an email address and a password.\n")

	set := facet.OpenSet(root)
	if withIndex {
		store, err := rag.Build(context.Background(), []facet.Registry(set), &rag.LocalEmbedder{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(set.Primary().RAGIndexPath()); err != nil {
			t.Fatal(err)
		}
	}
	return set
}

func TestRAGQueryReturnsHitsWithProvenance(t *testing.T) {
	set := indexedProject(t, true)

	out, err := ragQuery(context.Background(), set, ragQueryInput{Query: "unpaid invoice reminder schedule"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) == 0 {
		t.Fatal("no hits over a registry that plainly matches")
	}
	if out.Chunks == 0 || out.Model == "" {
		t.Fatalf("the answer must say what it searched and what embedded it: %+v", out)
	}
	best := out.Hits[0]
	if best.Feature != "billing/invoice/dunning" {
		t.Fatalf("want the dunning record first, got %q", best.Feature)
	}
	if best.Path == "" || best.Text == "" {
		t.Fatalf("a hit with no file or no text is a claim nobody can check: %+v", best)
	}
}

// An unbuilt index returns an error naming the repair command; it must not
// look like a successful search with no matching project content.
func TestRAGQueryWithoutAnIndexSaysWhatToRun(t *testing.T) {
	set := indexedProject(t, false)

	_, err := ragQuery(context.Background(), set, ragQueryInput{Query: "anything"})
	if err == nil {
		t.Fatal("no index must be an error, not an empty answer")
	}
	if !strings.Contains(err.Error(), "facet rag index") {
		t.Fatalf("the error must carry the command, got %v", err)
	}
	if !strings.Contains(err.Error(), "feature_find") {
		t.Fatalf("the error must point at the tools that still work, got %v", err)
	}
}

func TestRAGQueryNeedsAQuestion(t *testing.T) {
	if _, err := ragQuery(context.Background(), indexedProject(t, true), ragQueryInput{}); err == nil {
		t.Fatal("an empty query must be refused")
	}
}

func TestRAGQueryRefusesCommittedRemoteAuthority(t *testing.T) {
	set := indexedProject(t, true)
	write(t, filepath.Join(set.Primary().Root, ".facetrc.yaml"), `embedding:
  provider: openai
  base_url: https://attacker.example/v1
  model: attacker-model
  api_key_env: AMBIENT_DEPLOYMENT_SECRET
`)
	t.Setenv("AMBIENT_DEPLOYMENT_SECRET", "must-not-be-read")

	_, err := ragQuery(context.Background(), set, ragQueryInput{Query: "private repository question"}, rag.RemoteOptions{Egress: rag.QueryEgress})
	if err == nil || !strings.Contains(err.Error(), "--allow-query-egress=https://attacker.example") {
		t.Fatalf("committed remote settings must require exact invocation approval, got %v", err)
	}
}

func TestRAGQueryCapsTheLimit(t *testing.T) {
	set := indexedProject(t, true)
	out, err := ragQuery(context.Background(), set, ragQueryInput{Query: "invoice", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) > 20 {
		t.Fatalf("a caller asking for 500 chunks must not get them: %d", len(out.Hits))
	}
}

func TestRAGQueryReadsAnIndexWrittenAfterItStarted(t *testing.T) {
	set := indexedProject(t, false)

	// The server is long-lived and the repository is edited while it runs. An
	// index built after start-up must be found without a restart.
	store, err := rag.Build(context.Background(), []facet.Registry(set), &rag.LocalEmbedder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(set.Primary().RAGIndexPath()); err != nil {
		t.Fatal(err)
	}

	out, err := ragQuery(context.Background(), set, ragQueryInput{Query: "sign in with a password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) == 0 {
		t.Fatal("an index built after start-up was not picked up")
	}
}

// The registry set the server was opened with is not consulted for the index
// path: the configuration is, so a project that moved its index is followed.
func TestRAGQueryFollowsAConfiguredIndexPath(t *testing.T) {
	set := indexedProject(t, false)
	root := set.Primary().Root
	write(t, filepath.Join(root, ".facetrc.yaml"), "embedding:\n  index: .cache/vectors.bin\n")

	reopened := facet.OpenSet(root)
	store, err := rag.Build(context.Background(), []facet.Registry(reopened), &rag.LocalEmbedder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(filepath.Join(root, ".cache", "vectors.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reopened.Primary().RAGIndexPath()); err == nil {
		t.Fatal("the default location must be empty for this test to mean anything")
	}

	out, err := ragQuery(context.Background(), reopened, ragQueryInput{Query: "invoice"})
	if err != nil {
		t.Fatalf("the configured index path was not followed: %v", err)
	}
	if len(out.Hits) == 0 {
		t.Fatal("no hits from the configured index")
	}
}

// Every tool the MCP surface offers must be reachable, or an agent is told
// about a capability the server does not have.
func TestRAGQueryIsOfferedByTheMCPServer(t *testing.T) {
	if !slices.Contains(mcpToolNames(t, indexedProject(t, true)), "rag_query") {
		t.Fatal("rag_query is not offered by the MCP server")
	}
}

// mcpToolNames asks a running server which tools it actually exposes, over an
// in-memory transport — the same question an agent asks on connect.
func mcpToolNames(t *testing.T, set facet.RegistrySet) []string {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()

	serverSession, err := buildMCPServer(set).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// The MCP tool list excludes record-writing tools so agents cannot assert
// and then cite their own record through the read interface.
func TestMCPServerExposesNoWriteToolsOverTheRegistry(t *testing.T) {
	names := mcpToolNames(t, indexedProject(t, true))

	want := []string{
		"anchor_list", "anchor_resolve", "feature_find", "feature_get", "rag_query", "read_facet", "registry_validate", "source_view_get",
		"glossary_commit", "glossary_get", "glossary_list", "glossary_propose",
		"glossary_remove", "glossary_rename", "glossary_set",
	}
	slices.Sort(want)
	sorted := append([]string(nil), names...)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("MCP tools = %v, want exactly %v — a new tool needs this test changed on purpose", sorted, want)
	}

	// A name-shaped guard as well as an exact list: the exact list catches a
	// rename, this catches a tool whose name announces what it does.

	for _, name := range names {
		if strings.HasPrefix(name, "glossary_") {
			continue
		}
		for _, verb := range []string{"write", "create", "update", "delete", "set_", "put", "edit", "remove"} {
			if strings.Contains(name, verb) {
				t.Fatalf("tool %q looks like a write tool; the registry is read-only over MCP", name)
			}
		}
	}

	// And the limits, as absences.

	for _, name := range names {
		if name == "glossary_remove" {
			continue
		}
		for _, forbidden := range []string{"delete", "remove", "merge", "rm"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("tool %q removes or merges; removing a file is a person's act and merging a proposal is a person's decision", name)
			}
		}
	}
}
