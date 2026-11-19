package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func serverFixture(t *testing.T) (*Server, string) {
	t.Helper()
	root, pub := fixture(t)
	c, err := LoadCorpus(root, pub)
	if err != nil {
		t.Fatal(err)
	}
	token, key, err := GenerateKey([]string{RecordsScope, CodeScope, SearchScope}, []string{"rest", "mcp"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(c, Config{Origin: "https://facet.example", Keys: []Key{key}, MCPKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	return server, token
}
func request(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "https://facet.example"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}
func TestRESTAuthorizationAndReadBoundary(t *testing.T) {
	s, token := serverFixture(t)
	for _, tc := range []struct {
		path, credential string
		status           int
	}{{"/v1/record?id=navigation/interfaces/example", "", 401}, {"/v1/record?id=:navigation/interfaces/example", token, 200}, {"/v1/source?path=source.go&start=1&end=2", token, 200}, {"/v1/source?path=../secret&start=1&end=1", token, 404}, {"/v1/sections?id=navigation/interfaces/example&section=missing", token, 422}, {"/v1/record?id=navigation/interfaces/example&ID=other", token, 400}} {
		rec := request(t, s, tc.credential, "GET", tc.path, "")
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("content cacheable")
		}
	}
	s.auth.Keys[0].Scopes = []string{RecordsScope}
	rec := request(t, s, token, "GET", "/v1/source?path=source.go&start=1&end=1", "")
	if rec.Code != 403 {
		t.Fatal("source allowed without code scope")
	}
}
func TestHostOriginAndParserDefenses(t *testing.T) {
	s, token := serverFixture(t)
	for _, field := range []string{"host", "origin"} {
		req := httptest.NewRequest("GET", "https://facet.example/v1/records", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if field == "host" {
			req.Host = "attacker.example"
		} else {
			req.Header.Set("Origin", "https://attacker.example")
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 403 || rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("host/origin rejection broken", rec.Code)
		}
	}
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"tools/call","METHOD":"tools/list","params":{"name":"source_read","arguments":{}}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"Name":"source_read","arguments":{}}}`, `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`} {
		rec := request(t, s, token, "POST", "/mcp", body)
		if rec.Code != 400 {
			t.Fatalf("ambiguous RPC accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	s.auth.Keys[0].Scopes = []string{RecordsScope}
	rec := request(t, s, token, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"source_read","arguments":{"path":"source.go","start":1,"end":1}}}`)
	if rec.Code != 403 {
		t.Fatal("MCP scope check missing", rec.Code, rec.Body.String())
	}
}

type authenticatedTransport struct {
	base  http.RoundTripper
	token string
}

func (t authenticatedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Host = "facet.example"
	copy.Header = copy.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
func TestRealMCPClientReadOnlyCatalogAndCall(t *testing.T) {
	s, token := serverFixture(t)
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: authenticatedTransport{http.DefaultTransport, token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	caps := session.InitializeResult().Capabilities
	if caps.Logging != nil || caps.Tools == nil || caps.Tools.ListChanged || caps.Resources != nil {
		t.Fatal("unexpected capabilities", caps)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 7 {
		t.Fatal("unexpected tools", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if _, ok := scopes[tool.Name]; !ok || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("unapproved tool", tool.Name)
		}
	}
	empty, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "capabilities"})
	if err != nil || empty.IsError {
		t.Fatal("omitted arguments rejected", empty, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_get", Arguments: map[string]any{"id": ":navigation/interfaces/example"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP read failed %+v %v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if !bytes.Contains(raw, []byte("Example record")) {
		t.Fatal("record missing", string(raw))
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "record_sections", Arguments: map[string]any{"id": "navigation/interfaces/example", "sections": []string{"missing"}}})
	if err != nil || !result.IsError {
		t.Fatalf("expected structured domain failure %+v %v", result, err)
	}
}
func TestBodyAndRateLimits(t *testing.T) {
	s, token := serverFixture(t)
	large := strings.Repeat(" ", 65537)
	rec := request(t, s, token, "POST", "/v1/search", large)
	if rec.Code != 413 {
		t.Fatal("body not bounded", rec.Code)
	}
	for i := 0; i < 10; i++ {
		rec = request(t, s, token, "GET", "/v1/records", "")
	}
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit not enforced", rec.Code)
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1,"A":2}`, `{"a":1} {}`, `{"a":`} {
		if _, err := strictJSON([]byte(raw)); err == nil {
			t.Fatal("strict JSON accepted", raw)
		}
	}
}

func TestRequestURIBoundary(t *testing.T) {
	s, _ := serverFixture(t)
	for _, size := range []int{8191, 8192, 8193} {
		path := "/healthz?padding=" + strings.Repeat("x", size-len("/healthz?padding="))
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "facet.example"
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		want := 200
		if size > 8192 {
			want = 414
		}
		if rec.Code != want {
			t.Fatalf("URI length %d: got %d, want %d", size, rec.Code, want)
		}
	}
}

func TestGlobalInflightAdmission(t *testing.T) {
	s, token := serverFixture(t)
	if cap(s.inflight) != 16 {
		t.Fatal("inflight contract changed", cap(s.inflight))
	}
	for i := 0; i < cap(s.inflight); i++ {
		s.inflight <- struct{}{}
	}
	defer func() {
		for len(s.inflight) > 0 {
			<-s.inflight
		}
	}()
	denied := request(t, s, token, "GET", "/v1/capabilities", "")
	if denied.Code != 429 || !strings.Contains(denied.Body.String(), "concurrency_limited") {
		t.Fatal(denied.Code, denied.Body.String())
	}
	<-s.inflight
	if got := request(t, s, token, "GET", "/v1/capabilities", ""); got.Code != 200 {
		t.Fatal("released slot not usable", got.Code)
	}
}

func TestPreAuthenticationLimitAndPublicExemptions(t *testing.T) {
	s, _ := serverFixture(t)
	// Freeze refill while checking route dispatch; no clock-sensitive sleeps.
	s.pre = bucket{Tokens: 0, At: time.Now().Add(time.Hour)}
	denied := request(t, s, "", "GET", "/v1/capabilities", "")
	if denied.Code != 429 || !strings.Contains(denied.Body.String(), "rate_limited") {
		t.Fatal("limit must precede authentication", denied.Code, denied.Body.String())
	}
	s.config.OAuth = &OAuthConfig{Issuer: "https://issuer.example", ScopeMap: map[string]string{"read": RecordsScope}}
	for _, path := range []string{"/healthz", "/readyz", "/.well-known/oauth-protected-resource/mcp"} {
		if got := request(t, s, "", "GET", path, ""); got.Code != 200 {
			t.Fatal("public route consumed exhausted budget", path, got.Code)
		}
	}
	s.pre = bucket{}
	if got := request(t, s, "", "GET", "/v1/capabilities", ""); got.Code != 401 {
		t.Fatal("authentication bypassed after reset", got.Code)
	}
}

func TestMCPRejectsSessionTransports(t *testing.T) {
	s, token := serverFixture(t)
	for _, method := range []string{"GET", "DELETE"} {
		got := request(t, s, token, method, "/mcp", "")
		if got.Code != 405 || got.Header().Get("Allow") != "POST" {
			t.Fatal(method, got.Code, got.Header().Get("Allow"))
		}
	}
}
