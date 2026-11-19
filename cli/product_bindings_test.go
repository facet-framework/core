// @facet: navigation/interfaces/product-services
// @facet-role: test
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Two bindings exist only in this test binary, the way a build registers its
// product bindings from init: "aliased" keeps one MCP tool name, "optin" is
// requested through a section of .facetrc.yaml, has a placeholder command and
// a drift help paragraph. Core itself registers no binding.
func init() {
	RegisterProduct(ProductBinding{ID: "aliased", CompatibilityTools: []string{"find"}})
	RegisterProduct(ProductBinding{
		ID:        "optin",
		Command:   "optin-command",
		DriftHelp: "Optin drift paragraph.",
		Requested: func(service string, config func() (*facet.Config, error)) (bool, error) {
			if service != "drift-report" {
				return false, nil
			}
			cfg, err := config()
			if err != nil {
				return false, err
			}
			var section struct {
				On bool `yaml:"on"`
			}
			if _, err := cfg.ProductSection("optin", &section); err != nil {
				return false, err
			}
			return section.On, nil
		},
		Missing: "optin.on requires an installed optin product",
	})
}

func TestABindingKeepsOnlyItsOwnCompatibilityToolName(t *testing.T) {
	installServiceFixtureAs(t, "aliased", map[string]any{
		"mcp-tools": map[string]any{"tools": []any{
			map[string]any{"name": "find", "input_schema": map[string]any{"type": "object"}},
			map[string]any{"name": "other", "input_schema": map[string]any{"type": "object"}},
		}},
		"mcp-call": map[string]any{"found": true},
	})
	root := t.TempDir()
	server := buildMCPServer(facet.RegistrySet{{Root: root, RecordsDir: filepath.Join(root, "records")}})
	a, b := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if !names["find"] || names["aliased__find"] || !names["aliased__other"] {
		t.Fatalf("tool names: %v", names)
	}
	if result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "find", Arguments: map[string]any{}}); err != nil || result.IsError {
		t.Fatalf("call under the compatibility name: %+v %v", result, err)
	}
	if got := ProductToolName("fixture", "find"); got != "fixture__find" {
		t.Fatalf("another product claimed the compatibility name: %q", got)
	}
}

func TestARequestedProductThatIsAbsentIsReported(t *testing.T) {
	t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
	project := func(config string) string {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, facet.ConfigName), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	replies := invokeProducts(context.Background(), project("optin:\n  on: true\n"), "", "drift-report", struct{}{})
	if len(replies) != 1 || replies[0].ID != "optin" || replies[0].Err == nil || replies[0].Err.Error() != "optin.on requires an installed optin product" {
		t.Fatalf("requested product: %+v", replies)
	}
	if replies := invokeProducts(context.Background(), project("optin:\n  on: false\n"), "", "drift-report", struct{}{}); len(replies) != 0 {
		t.Fatalf("unrequested product created an error: %+v", replies)
	}
	if replies := invokeProducts(context.Background(), project("optin:\n  on: true\n"), "", "observe-session", struct{}{}); len(replies) != 0 {
		t.Fatalf("a service the binding does not request created an error: %+v", replies)
	}
	replies = invokeProducts(context.Background(), project("optin:\n  on: [\n"), "", "drift-report", struct{}{})
	if len(replies) != 1 || replies[0].ID != "configuration" || replies[0].Err == nil {
		t.Fatalf("unreadable configuration: %+v", replies)
	}
}

func TestABindingAddsItsPlaceholderAndDriftHelp(t *testing.T) {
	t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
	cmd := NewCommand(Options{})
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--root", t.TempDir(), "drift", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "product_errors") || !strings.HasSuffix(strings.TrimSpace(out.String()[:strings.Index(out.String(), "Usage:")]), "Optin drift paragraph.") {
		t.Fatalf("drift help:\n%s", out.String())
	}
	cmd = NewCommand(Options{})
	cmd.SetArgs([]string{"--root", t.TempDir(), "optin-command"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "requires the optin product") || !strings.Contains(err.Error(), "facet plugin product add") {
		t.Fatalf("placeholder: %v", err)
	}
}

func TestAProductCannotBeBoundTwice(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a second binding for one product was accepted")
		}
	}()
	RegisterProduct(ProductBinding{ID: "aliased"})
}
