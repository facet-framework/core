// @facet: navigation/interfaces/product-services
// @facet-role: test
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/facet-framework/core/pkg/productplugin"
	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installServiceFixture(t *testing.T, responses map[string]any) {
	t.Helper()
	installServiceFixtureAs(t, "fixture", responses)
}

// installServiceFixtureAs installs a shell product with the given manifest id
// that answers each named service with its canned result.
func installServiceFixtureAs(t *testing.T, id string, responses map[string]any) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell service fixture")
	}
	t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
	dir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\ncase \"$2\" in\n"
	for service, value := range responses {
		raw, err := json.Marshal(map[string]any{"protocol": 1, "result": value})
		if err != nil {
			t.Fatal(err)
		}
		script += service + ") printf '%s' '" + strings.ReplaceAll(string(raw), "'", "'\\''") + "';;\n"
	}
	script += "esac\n"
	digest := sha256.Sum256([]byte(script))
	services := []string{}
	for name := range responses {
		services = append(services, name)
	}
	manifest := productplugin.Manifest{Kind: "product", ID: id, Version: "1.0.0", Protocol: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "run", Commands: []string{id}, Services: services, Env: []string{"PATH"}, Files: map[string]string{"run": hex.EncodeToString(digest[:])}}
	raw, _ := json.Marshal(manifest)
	for name, body := range map[string][]byte{"run": []byte(script), "plugin.json": raw} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := productStore().Install(context.Background(), dir, true, false); err != nil {
		t.Fatal(err)
	}
}
func TestProductDriftAdditionsCannotOverwriteCore(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "addition", true: "collision"}[collision], func(t *testing.T) {
			field := "extra"
			if collision {
				field = "baseline"
			}
			installServiceFixture(t, map[string]any{"drift-report": map[string]any{"fields": map[string]any{field: "product"}, "text": "extra report"}})
			root := t.TempDir()
			regs := []facet.Registry{{Root: root}}
			report := facet.DriftReport{Baseline: facet.Baseline{Status: facet.BaselineOK, Commit: "abc"}}
			var out bytes.Buffer
			if err := writeProductDriftReport(&out, root, regs, report, true); err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got["baseline"], []byte("abc")) {
				t.Fatal("baseline replaced")
			}
			if collision && got["product_errors"] == nil {
				t.Fatal("collision hidden")
			}
			if !collision && got["extra"] == nil {
				t.Fatal("addition missing")
			}
		})
	}
}
func TestProductStopNoticeDeduplicatesPerRepository(t *testing.T) {
	installServiceFixture(t, map[string]any{"stop-notices": map[string]any{"groups": []any{map[string]any{"header": "Header", "footer": "Footer", "notices": []any{map[string]string{"key": "one", "text": "Notice"}}}}}})
	t.Setenv("HOME", t.TempDir())
	session := t.Name()
	root := t.TempDir()
	ctx := hookContext{root: root, registries: []facet.Registry{{Root: root}}}
	first := productStopNotices(ctx, session, facet.Baseline{})
	if len(first) != 1 || !strings.Contains(first[0], "Notice") {
		t.Fatalf("first: %v", first)
	}
	if got := productStopNotices(ctx, session, facet.Baseline{}); len(got) != 0 {
		t.Fatalf("duplicate: %v", got)
	}
	ctx.root = t.TempDir()
	if got := productStopNotices(ctx, session, facet.Baseline{}); len(got) != 1 {
		t.Fatalf("other repository suppressed: %v", got)
	}
}
func TestProductMCPNamespacedAndRevoked(t *testing.T) {
	installServiceFixture(t, map[string]any{"mcp-tools": map[string]any{"tools": []any{map[string]any{"name": "find", "input_schema": map[string]any{"type": "object"}}, map[string]any{"name": "broken", "input_schema": map[string]any{"type": "string"}}}}, "mcp-call": map[string]any{"found": true}})
	root := t.TempDir()
	server := buildMCPServer(facet.RegistrySet{{Root: root, RecordsDir: filepath.Join(root, "records")}})
	a, b := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "fixture__find", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("call: %+v %v", result, err)
	}
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "fixture__broken" {
			t.Fatal("invalid schema accepted")
		}
	}
	if err := productStore().Remove(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "fixture__find", Arguments: map[string]any{}})
	if err == nil && !result.IsError {
		t.Fatal("removed product executed")
	}
}
