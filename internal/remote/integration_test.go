package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type grantedProvider struct{}

func (grantedProvider) Verify(context.Context, string) (Principal, error) {
	return Principal{ID: "fixture", Scopes: []string{RecordsScope}, OAuth: true}, nil
}
func TestUIOAuthStepUpAndPublicMetadata(t *testing.T) {
	s, _ := serverFixture(t)
	s.config.UI = true
	var audit AuditEvent
	s.config.Audit = func(e AuditEvent) { audit = e }
	s.config.OAuth = &OAuthConfig{Issuer: "https://issuer.example", ScopeMap: map[string]string{"records": RecordsScope, "code": CodeScope}}
	s.auth.OAuth = grantedProvider{}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"source_read","arguments":{"path":"source.go","start":1,"end":1}}}`
	r := request(t, s, "oauth-fixture", "POST", "/mcp", body)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "mcp/www_authenticate") || strings.Contains(r.Body.String(), "Example record") {
		t.Fatal(r.Code, r.Body.String())
	}
	if audit.Code != "insufficient_scope" || audit.Status != 200 || strings.Contains(r.Body.String(), "structuredContent") {
		t.Fatal("tool denial audit/schema", audit, r.Body.String())
	}
	s.config.UI = false
	r = request(t, s, "oauth-fixture", "POST", "/mcp", body)
	if r.Code != 403 {
		t.Fatal("generic scope response", r.Code)
	}
	req := httptest.NewRequest("GET", "https://facet.example/.well-known/oauth-protected-resource/mcp", nil)
	req.Header.Set("Origin", "https://host.example")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("public metadata CORS", rec.Code)
	}
}
func TestInputContractsRejectUnrelatedFieldsAndExplicitZero(t *testing.T) {
	for _, path := range []string{"/v1/records?limit=0", "/v1/record?id=:navigation/interfaces/example&query=unused", "/v1/source?path=source.go&start=0&end=1", "/v1/capabilities?kind=features"} {
		s, key := serverFixture(t)
		r := request(t, s, key, "GET", path, "")
		if r.Code != 422 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
}

// withViewer registers page as this build's viewer for the duration of one
// test. The real page lives outside this package and is registered only by a
// build that registers a viewer page, so these tests use a page of their own.
func withViewer(t *testing.T, page string) {
	t.Helper()
	viewerRegistry.Lock()
	previous := viewerRegistry.html
	viewerRegistry.html = page
	viewerRegistry.Unlock()
	t.Cleanup(func() {
		viewerRegistry.Lock()
		viewerRegistry.html = previous
		viewerRegistry.Unlock()
	})
}

const fixtureViewer = "<!doctype html><title>fixture viewer</title>"

func TestMCPViewerAndSchemaViaSDK(t *testing.T) {
	withViewer(t, fixtureViewer)
	base, token := serverFixture(t)
	cfg := base.config
	cfg.UI = true
	s, err := NewServer(base.reader.corpus, cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "viewer-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: authenticatedTransport{http.DefaultTransport, token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	resources, err := session.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != viewerURI {
		t.Fatal(resources, err)
	}
	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: viewerURI})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if len(raw) > 1048576 || result.Contents[0].MIMEType != viewerMIME || result.Contents[0].Text != fixtureViewer {
		t.Fatal("invalid viewer resource size/type/page", len(raw))
	}
	_, err = session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "file:///etc/passwd"})
	if err == nil {
		t.Fatal("unapproved resource")
	}
	tool, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_get", Arguments: map[string]any{"id": ":navigation/interfaces/example", "query": "ignored"}})
	if err != nil || !tool.IsError || tool.StructuredContent != nil {
		t.Fatal("operation-specific input not enforced", tool, err)
	}
}

// mcpCall posts one JSON-RPC request to the server's real HTTP handler and
// decodes the response envelope.
func mcpCall(t *testing.T, s *Server, token, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("POST", "https://facet.example/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("status", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal("not one JSON-RPC envelope:", err, rec.Body.String())
	}
	return envelope
}

// A build that registered no viewer (facet-core) and an operator config with
// 'ui: true': the server must not advertise an MCP Apps resource it cannot
// serve. It offers no resources capability, links no tool to a ui:// resource,
// and answers resources/* with JSON-RPC Method not found instead of an empty
// page or a panic.
func TestUIWithoutRegisteredViewerOffersNoAppResource(t *testing.T) {
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"no-viewer","version":"1"}}}`
	const toolsList = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	newUIServer := func() (*Server, string) {
		base, token := serverFixture(t)
		cfg := base.config
		cfg.UI = true
		s, err := NewServer(base.reader.corpus, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return s, token
	}

	// Precondition: the same fixture with a registered page does serve the
	// resource, so its absence below comes from the missing page alone.
	withViewer(t, fixtureViewer)
	s, token := newUIServer()
	read := mcpCall(t, s, token, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"`+viewerURI+`"}}`)
	if !strings.Contains(fmt.Sprint(read["result"]), "fixture viewer") {
		t.Fatal("precondition: registered viewer not served", read)
	}

	viewerRegistry.Lock()
	viewerRegistry.html = ""
	viewerRegistry.Unlock()
	if RegisteredViewer() != "" {
		t.Fatal("precondition: viewer still registered")
	}
	s, token = newUIServer()

	initialized := mcpCall(t, s, token, initialize)
	result, _ := initialized["result"].(map[string]any)
	capabilities, _ := result["capabilities"].(map[string]any)
	if capabilities == nil || capabilities["tools"] == nil {
		t.Fatal("initialize did not answer with capabilities", initialized)
	}
	if _, present := capabilities["resources"]; present {
		t.Fatal("resources capability advertised without a viewer", capabilities)
	}

	listed, _ := mcpCall(t, s, token, toolsList)["result"].(map[string]any)
	tools, _ := listed["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	for _, tool := range tools {
		entry := tool.(map[string]any)
		if meta, _ := entry["_meta"].(map[string]any); meta != nil && meta["ui"] != nil {
			t.Fatal("tool links a viewer this build does not carry", entry["name"], meta)
		}
	}

	for _, method := range []string{`"resources/list"`, `"resources/read","params":{"uri":"` + viewerURI + `"}`, `"resources/templates/list"`} {
		answer := mcpCall(t, s, token, `{"jsonrpc":"2.0","id":4,"method":`+method+`}`)
		failure, _ := answer["error"].(map[string]any)
		if failure == nil || failure["code"] != float64(-32601) || answer["result"] != nil {
			t.Fatal(method, "expected JSON-RPC Method not found", answer)
		}
	}
}
