// @facet: navigation/interfaces/product-services
// @facet-role: primary
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/facet-framework/core/pkg/productplugin"
	"github.com/facet-framework/core/registry"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"regexp"
	"strings"
	"time"
)

// Native tools use a product namespace. A product binding may keep a tool's
// unprefixed name for compatibility (ProductToolName); only that product's
// replies can claim it.
func addProductMCPTools(server *mcp.Server, set facet.RegistrySet) {
	if len(set) == 0 {
		return
	}
	root := set.Primary().Root
	view := productView(set)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for _, reply := range invokeProducts(ctx, root, view, "mcp-tools", struct{}{}) {
		var result struct {
			Tools []struct {
				Name         string         `json:"name"`
				Description  string         `json:"description"`
				InputSchema  map[string]any `json:"input_schema"`
				OutputSchema map[string]any `json:"output_schema,omitempty"`
			} `json:"tools"`
		}
		err := reply.Err
		if err == nil {
			err = json.Unmarshal(reply.Payload, &result)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "facet product %s: MCP discovery failed: %v\n", reply.ID, err)
			continue
		}
		for _, tool := range result.Tools {
			name := ProductToolName(reply.ID, tool.Name)
			if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) || strings.HasPrefix(name, "plugin_") || seen[name] || tool.InputSchema["type"] != "object" {
				fmt.Fprintf(os.Stderr, "facet product %s: invalid or duplicate MCP tool %q\n", reply.ID, name)
				continue
			}
			input, err := resolveProductSchema(tool.InputSchema)
			if err != nil {
				fmt.Fprintf(os.Stderr, "facet product %s: invalid input schema: %v\n", reply.ID, err)
				continue
			}
			var output any
			if tool.OutputSchema != nil {
				schema, err := resolveProductSchema(tool.OutputSchema)
				if err != nil {
					fmt.Fprintf(os.Stderr, "facet product %s: invalid output schema: %v\n", reply.ID, err)
					continue
				}
				output = schema
			}
			seen[name] = true
			id, method := reply.ID, tool.Name
			mcp.AddTool(server, &mcp.Tool{Name: name, Description: tool.Description, InputSchema: input, OutputSchema: output}, func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
				payload, err := json.Marshal(struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}{method, args})
				if err != nil {
					return nil, nil, err
				}
				body, err := productStore().Call(ctx, id, "mcp-call", payload, productplugin.Invocation{Root: root, SourceView: view})
				if err != nil {
					return nil, nil, err
				}
				var value any
				if err = json.Unmarshal(body, &value); err != nil {
					return nil, nil, err
				}
				return nil, value, nil
			})
		}
	}
}
func resolveProductSchema(value map[string]any) (*jsonschema.Schema, error) {
	if value["type"] != "object" {
		return nil, fmt.Errorf("tool schema must describe an object")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err = json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if _, err = schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true}); err != nil {
		return nil, err
	}
	return &schema, nil
}
