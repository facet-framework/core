package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	contract "github.com/facet-framework/core/docs/integrations"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Config struct {
	Audit          func(AuditEvent) `json:"-"`
	UI             bool             `json:"ui"`
	RAG            *RAGConfig       `json:"rag,omitempty"`
	Origin         string           `json:"origin"`
	AllowedOrigins []string         `json:"allowed_origins"`
	Keys           []Key            `json:"keys"`
	MCPKeys        bool             `json:"mcp_api_keys"`
	OAuth          *OAuthConfig     `json:"oauth,omitempty"`
}
type Server struct {
	retrieval *Retrieval
	reader    *Reader
	auth      Authenticator
	config    Config
	host      string
	mcp       http.Handler
	inflight  chan struct{}
	mu        sync.Mutex
	pre       bucket
	rates     map[string]bucket
	spec      map[string]any
	inputs    map[string]*jsonschema.Schema
	// viewer is the MCP Apps page this server serves; empty when the build
	// registered none (see RegisterViewer).
	viewer string
}
type bucket struct {
	Tokens float64
	At     time.Time
}
type principalContext struct{}
type requestIDContext struct{}

var operations = map[string]string{"/v1/capabilities": "capabilities", "/v1/records": "records_find", "/v1/record": "record_get", "/v1/sections": "record_sections", "/v1/anchors": "anchors_list", "/v1/source": "source_read", "/v1/search": "records_search"}
var scopes = map[string][]string{"capabilities": {RecordsScope}, "records_find": {RecordsScope}, "record_get": {RecordsScope}, "record_sections": {RecordsScope}, "anchors_list": {RecordsScope, CodeScope}, "source_read": {CodeScope}, "records_search": {RecordsScope, SearchScope}}

func NewServer(c *Corpus, cfg Config) (*Server, error) {
	if c == nil || !httpsURL(cfg.Origin) {
		return nil, errors.New("published corpus and canonical HTTPS origin required")
	}
	u, _ := url.Parse(cfg.Origin)
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("origin must have no path")
	}
	cfg.Origin = strings.TrimSuffix(cfg.Origin, "/")
	for _, origin := range cfg.AllowedOrigins {
		v, err := url.Parse(origin)
		if err != nil || !httpsURL(origin) || v.Path != "" {
			return nil, errors.New("allowed origins must be exact HTTPS origins")
		}
	}
	a := Authenticator{Keys: cfg.Keys, MCPKeys: cfg.MCPKeys}
	if cfg.OAuth != nil {
		if cfg.OAuth.Audience == "" {
			cfg.OAuth.Audience = cfg.Origin + "/mcp"
		}
		provider, err := NewOAuthProvider(*cfg.OAuth)
		if err != nil {
			return nil, err
		}
		a.OAuth = provider
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	s := &Server{reader: NewReader(c), auth: a, config: cfg, host: u.Host, inflight: make(chan struct{}, 16), rates: map[string]bucket{}, viewer: RegisteredViewer()}
	if json.Unmarshal(contract.OpenAPI, &s.spec) != nil {
		return nil, errors.New("invalid embedded OpenAPI")
	}
	retrieval, err := loadRetrieval(c, cfg.RAG)
	if err != nil {
		return nil, err
	}
	s.retrieval = retrieval
	s.inputs = map[string]*jsonschema.Schema{}
	s.spec["servers"] = []any{map[string]any{"url": cfg.Origin}}
	capabilities := &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}
	if s.viewerEnabled() {
		capabilities.Resources = &mcp.ResourceCapabilities{}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "facet-read", Version: "0.1.0"}, &mcp.ServerOptions{Capabilities: capabilities})
	names := []string{}
	for name := range scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		input, output, description := s.toolSchema(name)
		compiler := jsonschema.NewCompiler()
		schemaURL := "https://facet.invalid/input/" + name
		if err := compiler.AddResource(schemaURL, s.expand(input)); err != nil {
			return nil, err
		}
		compiled, err := compiler.Compile(schemaURL)
		if err != nil {
			return nil, err
		}
		s.inputs[name] = compiled
		no := false
		tool := &mcp.Tool{Name: name, Description: description, InputSchema: input, OutputSchema: output, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no, OpenWorldHint: &no}}
		if name == "records_search" {
			tool.Annotations.OpenWorldHint = &retrieval.egress
		}
		if cfg.OAuth != nil {
			tool.Meta = mcp.Meta{"securitySchemes": []any{map[string]any{"type": "oauth2", "scopes": s.externalScopes(append([]string{RecordsScope}, scopes[name]...))}}}
		}
		if s.viewerEnabled() && slices.Contains([]string{"records_find", "record_get", "records_search"}, name) {
			if tool.Meta == nil {
				tool.Meta = mcp.Meta{}
			}
			tool.Meta["ui"] = map[string]any{"resourceUri": viewerURI, "visibility": []string{"model", "app"}}
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			principal, ok := ctx.Value(principalContext{}).(Principal)
			if !ok || !hasScopes(principal, scopes[name]) {
				return toolProblem(ctx, problem(403, "insufficient_scope")), nil
			}
			arguments := req.Params.Arguments
			if len(arguments) == 0 || string(arguments) == "null" {
				arguments = json.RawMessage(`{}`)
			}
			value, err := strictJSON(arguments)
			if err != nil || s.inputs[name].Validate(value) != nil {
				return toolProblem(ctx, problem(422, "invalid_selection")), nil
			}
			var in Input
			dec := json.NewDecoder(bytes.NewReader(arguments))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil {
				return toolProblem(ctx, problem(400, "invalid_request")), nil
			}
			out, err := s.read(ctx, name, in)
			if err != nil {
				p := asProblem(err)
				p.RequestID, _ = ctx.Value(requestIDContext{}).(string)
				return toolProblem(ctx, p), nil
			}
			raw, err := json.Marshal(out)
			if err != nil {
				return toolProblem(ctx, problem(400, "invalid_request")), nil
			}
			result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: out}
			wire, err := json.Marshal(result)
			if err != nil || len(wire) > 1048576-16384 {
				return toolProblem(ctx, tooLarge()), nil
			}
			return result, nil
		})
	}
	if s.viewerEnabled() {
		s.addViewer(server)
	}
	s.mcp = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	return s, nil
}
func toolProblem(ctx context.Context, p *Problem) *mcp.CallToolResult {
	if event, ok := ctx.Value(auditContext{}).(*AuditEvent); ok {
		event.Code = p.Code
	}
	p.RequestID, _ = ctx.Value(requestIDContext{}).(string)
	raw, _ := json.Marshal(p)
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}
func hasScopes(p Principal, wanted []string) bool {
	for _, scope := range wanted {
		if !slices.Contains(p.Scopes, scope) {
			return false
		}
	}
	return true
}
func tooLarge() *Problem {
	p := problem(422, "response_too_large")
	p.Hint = "Request selected record sections or a smaller source range."
	return p
}
func asProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		copy := *p
		return &copy
	}
	return problem(400, "invalid_request")
}
func (s *Server) externalScopes(internal []string) []string {
	out := []string{}
	if s.config.OAuth != nil {
		for external, permission := range s.config.OAuth.ScopeMap {
			if slices.Contains(internal, permission) {
				out = append(out, external)
			}
		}
	}
	sort.Strings(out)
	return out
}
func (s *Server) challenge(w http.ResponseWriter, r *http.Request, p *Problem, wanted []string) {
	if p.Status != 401 && p.Code != "insufficient_scope" {
		return
	}
	value := "Bearer"
	if p.Status == 403 {
		value += ` error="insufficient_scope", scope=` + strconv.Quote(strings.Join(wanted, " "))
	}
	if r.URL.Path == "/mcp" && s.config.OAuth != nil && !strings.HasPrefix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "facet_k_") {
		value = "Bearer resource_metadata=" + strconv.Quote(s.config.Origin+"/.well-known/oauth-protected-resource/mcp")
		requested := s.externalScopes(wanted)
		if len(requested) > 0 {
			value += ", scope=" + strconv.Quote(strings.Join(requested, " "))
		}
		if p.Status == 403 {
			value += `, error="insufficient_scope"`
		}
	}
	w.Header().Set("WWW-Authenticate", value)
}
func (s *Server) failure(w http.ResponseWriter, r *http.Request, p *Problem, wanted []string) {
	if event, ok := r.Context().Value(auditContext{}).(*AuditEvent); ok {
		event.Code = p.Code
	}
	p.RequestID, _ = r.Context().Value(requestIDContext{}).(string)
	s.challenge(w, r, p, wanted)
	if p.Status == 429 || p.Code == "auth_provider_unavailable" || p.Code == "provider_unavailable" {
		w.Header().Set("Retry-After", "5")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	json.NewEncoder(w).Encode(p)
}
func (s *Server) allow(id string, global bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	rate, burst := 1.0, 10.0
	b := s.rates[id]
	if global {
		rate, burst = 100, 100
		b = s.pre
	}
	if b.At.IsZero() {
		b.Tokens = burst
	} else {
		b.Tokens = min(burst, b.Tokens+now.Sub(b.At).Seconds()*rate)
	}
	b.At = now
	ok := b.Tokens >= 1
	if ok {
		b.Tokens--
	}
	if global {
		s.pre = b
	} else {
		if len(s.rates) >= 10000 {
			for key, old := range s.rates {
				if now.Sub(old.At) > time.Minute {
					delete(s.rates, key)
				}
			}
			if _, exists := s.rates[id]; !exists && len(s.rates) >= 10000 {
				return false
			}
		}
		s.rates[id] = b
	}
	return ok
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var random [16]byte
	rand.Read(random[:])
	id := hex.EncodeToString(random[:])
	event := &AuditEvent{RequestID: id, Operation: "other", Snapshot: s.reader.corpus.Snapshot}
	if op, ok := operations[r.URL.Path]; ok {
		event.Operation = op
	} else if r.URL.Path == "/mcp" {
		event.Operation = "mcp"
	}
	observed := &statusWriter{ResponseWriter: w}
	w = observed
	defer s.audit(observed, event, time.Now())
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	ctx = context.WithValue(ctx, auditContext{}, event)
	defer cancel()
	ctx = context.WithValue(ctx, requestIDContext{}, id)
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(p *Problem, required []string) { s.failure(w, r, p, required) }
	if r.Host != s.host {
		fail(problem(403, "host_forbidden"), nil)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.config.Origin && !slices.Contains(s.config.AllowedOrigins, origin) && r.URL.Path != "/.well-known/oauth-protected-resource/mcp" {
		fail(problem(403, "origin_forbidden"), nil)
		return
	}
	headerBytes := len("Host") + len(r.Host) + 4
	for name, values := range r.Header {
		for _, value := range values {
			headerBytes += len(name) + len(value) + 4
		}
	}
	if headerBytes > 16<<10 {
		fail(problem(431, "headers_too_large"), nil)
		return
	}
	if len(r.RequestURI) > 8192 {
		fail(problem(414, "uri_too_long"), nil)
		return
	}
	public := r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/.well-known/oauth-protected-resource/mcp"
	if !public && !s.allow("", true) {
		fail(problem(429, "rate_limited"), nil)
		return
	}
	if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		if r.Method != "GET" {
			fail(problem(405, "method_not_allowed"), nil)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/openapi.json" {
		if r.Method != "GET" {
			fail(problem(405, "method_not_allowed"), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.spec)
		return
	}
	if r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
		if s.config.OAuth == nil {
			fail(problem(404, "not_found"), nil)
			return
		}
		if r.Method != "GET" {
			fail(problem(405, "method_not_allowed"), nil)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, map[string]any{"resource": s.config.Origin + "/mcp", "authorization_servers": []string{s.config.OAuth.Issuer}, "scopes_supported": s.externalScopes([]string{RecordsScope, CodeScope, SearchScope}), "bearer_methods_supported": []string{"header"}})
		return
	}
	operation, exists := operations[r.URL.Path]
	transport := "rest"
	required := scopes[operation]
	if r.URL.Path == "/mcp" {
		transport = "mcp"
		required = []string{RecordsScope}
	} else if !exists {
		fail(problem(404, "not_found"), nil)
		return
	}
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 {
		fail(problem(401, "invalid_token"), required)
		return
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		fail(problem(401, "invalid_token"), required)
		return
	}
	principal, err := s.auth.Verify(ctx, parts[1], transport)
	if err != nil {
		fail(asProblem(err), required)
		return
	}
	principalHash := sha256.Sum256([]byte(principal.ID))
	event.Principal = hex.EncodeToString(principalHash[:])
	if !hasScopes(principal, required) {
		fail(problem(403, "insufficient_scope"), required)
		return
	}
	if !s.allow(principal.ID, false) {
		fail(problem(429, "rate_limited"), nil)
		return
	}
	select {
	case s.inflight <- struct{}{}:
		defer func() { <-s.inflight }()
	default:
		fail(problem(429, "concurrency_limited"), nil)
		return
	}
	ctx = context.WithValue(ctx, principalContext{}, principal)
	r = r.WithContext(ctx)
	if transport == "mcp" {
		s.serveMCP(w, r, principal)
		return
	}
	method := "GET"
	if operation == "records_search" {
		method = "POST"
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(problem(405, "method_not_allowed"), nil)
		return
	}
	in, err := s.restInput(r, operation)
	if err != nil {
		fail(asProblem(err), nil)
		return
	}
	out, err := s.read(ctx, operation, in)
	if err != nil {
		fail(asProblem(err), nil)
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		fail(problem(400, "invalid_request"), nil)
		return
	}
	if len(raw) > 1048576 {
		fail(tooLarge(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func (s *Server) restInput(r *http.Request, operation string) (Input, error) {
	var in Input
	if r.Method == "POST" {
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			return in, problem(415, "unsupported_media_type")
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(raw) > 65536 {
			return in, problem(413, "request_too_large")
		}
		value, err := strictJSON(raw)
		if err != nil {
			return in, problem(400, "invalid_request")
		}
		obj, ok := value.(map[string]any)
		if !ok {
			return in, problem(400, "invalid_request")
		}
		for key := range obj {
			if key != "query" && key != "limit" {
				return in, problem(400, "invalid_request")
			}
		}
		if s.inputs[operation].Validate(obj) != nil {
			return in, problem(422, "invalid_selection")
		}
		if json.Unmarshal(raw, &in) != nil {
			return in, problem(400, "invalid_request")
		}
		return in, nil
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return in, problem(400, "invalid_request")
	}
	obj := map[string]any{}
	for key, values := range q {
		schemaKey := key
		if key == "section" {
			schemaKey = "sections"
		}
		if key == "section" {
			obj[schemaKey] = values
		} else {
			obj[schemaKey] = values[0]
		}
		if key != "section" && len(values) != 1 {
			return in, problem(400, "invalid_request")
		}
		switch key {
		case "id":
			in.ID = values[0]
		case "kind":
			in.Kind = values[0]
		case "query":
			in.Query = values[0]
		case "cursor":
			in.Cursor = values[0]
		case "path":
			in.Path = values[0]
		case "section":
			in.Sections = values
		case "limit", "start", "end":
			value, e := strconv.Atoi(values[0])
			if e != nil {
				return in, problem(422, "invalid_selection")
			}
			obj[key] = value
			switch key {
			case "limit":
				in.Limit = value
			case "start":
				in.Start = value
			case "end":
				in.End = value
			}
		default:
			return in, problem(400, "invalid_request")
		}
	}
	// Convert typed slices and numbers to the JSON data model used by the validator.
	raw, _ := json.Marshal(obj)
	var value any
	json.Unmarshal(raw, &value)
	if s.inputs[operation].Validate(value) != nil {
		return in, problem(422, "invalid_selection")
	}
	return in, nil
}
func (s *Server) read(ctx context.Context, operation string, in Input) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, problem(503, "provider_unavailable")
	}
	switch operation {
	case "capabilities":
		limits := map[string]any{}
		schemas := s.spec["components"].(map[string]any)["schemas"].(map[string]any)
		props := schemas["Limits"].(map[string]any)["properties"].(map[string]any)
		for k, v := range props {
			limits[k] = v.(map[string]any)["const"]
		}
		return map[string]any{"snapshot": s.reader.corpus.Snapshot, "rag": s.retrieval.state, "provider": s.ragProvider(), "model": s.ragModel(), "query_egress": s.retrieval.egress, "limits": limits}, nil
	case "records_find":
		return s.reader.find(in)
	case "record_get":
		return s.reader.corpus.record(in.ID, in.Kind)
	case "record_sections":
		return s.reader.sections(in)
	case "anchors_list":
		return s.reader.anchors(in)
	case "source_read":
		return s.reader.source(in)
	case "records_search":
		return s.retrieval.search(ctx, s.reader.corpus.Snapshot, in)
	default:
		return nil, problem(404, "not_found")
	}
}
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, p Principal) {
	fail := func(e *Problem, required []string) { s.failure(w, r, e, required) }
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		fail(problem(405, "method_not_allowed"), nil)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(raw) > 65536 {
		fail(problem(413, "request_too_large"), nil)
		return
	}
	value, err := strictJSON(raw)
	envelope, ok := value.(map[string]any)
	if err != nil || !ok {
		fail(problem(400, "invalid_request"), nil)
		return
	}
	if envelope["jsonrpc"] != "2.0" {
		fail(problem(400, "invalid_request"), nil)
		return
	}
	if id, present := envelope["id"]; present {
		valid := false
		switch value := id.(type) {
		case string:
			valid = len(value) <= 256
		case json.Number:
			_, err := value.Int64()
			valid = err == nil
		}
		if !valid {
			fail(problem(400, "invalid_request"), nil)
			return
		}
	}
	for key := range envelope {
		if !slices.Contains([]string{"jsonrpc", "id", "method", "params"}, key) {
			fail(problem(400, "invalid_request"), nil)
			return
		}
	}
	method, _ := envelope["method"].(string)
	allowed := slices.Contains([]string{"initialize", "ping", "notifications/initialized", "tools/list", "tools/call"}, method)
	if s.viewerEnabled() && slices.Contains([]string{"resources/list", "resources/read", "resources/templates/list"}, method) {
		allowed = true
	}
	if !allowed {
		if envelope["jsonrpc"] == "2.0" && envelope["id"] != nil {
			writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": envelope["id"], "error": map[string]any{"code": -32601, "message": "Method not found"}})
		} else {
			fail(problem(400, "invalid_request"), nil)
		}
		return
	}
	if method == "tools/call" {
		params, ok := envelope["params"].(map[string]any)
		if !ok {
			fail(problem(400, "invalid_request"), nil)
			return
		}
		for key := range params {
			if !slices.Contains([]string{"name", "arguments", "_meta"}, key) {
				fail(problem(400, "invalid_request"), nil)
				return
			}
		}
		name, _ := params["name"].(string)
		if event, ok := r.Context().Value(auditContext{}).(*AuditEvent); ok {
			if _, known := scopes[name]; known {
				event.Operation = name
			}
		}
		wanted, exists := scopes[name]
		if exists && !hasScopes(p, wanted) {
			if s.config.UI && p.OAuth && envelope["jsonrpc"] == "2.0" && envelope["id"] != nil {
				challenge := "Bearer error=\"insufficient_scope\", error_description=\"Additional read permission is required\", scope=" + strconv.Quote(strings.Join(s.externalScopes(append([]string{RecordsScope}, wanted...)), " ")) + ", resource_metadata=" + strconv.Quote(s.config.Origin+"/.well-known/oauth-protected-resource/mcp")
				result := toolProblem(r.Context(), problem(403, "insufficient_scope"))
				result.Meta = mcp.Meta{"mcp/www_authenticate": []string{challenge}}
				writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": envelope["id"], "result": result})
				return
			}
			fail(problem(403, "insufficient_scope"), wanted)
			return
		}
	}
	canonical, err := json.Marshal(envelope)
	if err != nil {
		fail(problem(400, "invalid_request"), nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(canonical))
	r.ContentLength = int64(len(canonical))
	s.mcp.ServeHTTP(w, r)
}
func strictJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := jsonValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}
func jsonValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("JSON nesting limit")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		obj := map[string]any{}
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key")
			}
			fold := strings.ToLower(key)
			if seen[fold] {
				return nil, errors.New("duplicate JSON key")
			}
			seen[fold] = true
			value, err := jsonValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			obj[key] = value
		}
		_, err = dec.Token()
		return obj, err
	case '[':
		values := []any{}
		for dec.More() {
			value, err := jsonValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = dec.Token()
		return values, err
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}
func (s *Server) toolSchema(name string) (any, any, string) {
	paths := s.spec["paths"].(map[string]any)
	for route, opName := range operations {
		if opName != name {
			continue
		}
		method := "get"
		if name == "records_search" {
			method = "post"
		}
		operation := paths[route].(map[string]any)[method].(map[string]any)
		properties := map[string]any{}
		required := []any{}
		input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if params, ok := operation["parameters"].([]any); ok {
			for _, raw := range params {
				param := raw.(map[string]any)
				key := param["name"].(string)
				if key == "section" {
					key = "sections"
				}
				properties[key] = param["schema"]
				if param["required"] == true {
					required = append(required, key)
				}
			}
		}
		input["required"] = required
		if name == "records_search" {
			input = s.expand(operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]).(map[string]any)
		}
		output := operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
		return input, s.expand(output), operation["summary"].(string)
	}
	panic(fmt.Sprintf("unknown tool %s", name))
}
func (s *Server) expand(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			return s.expand(s.spec["components"].(map[string]any)["schemas"].(map[string]any)[name])
		}
		out := map[string]any{}
		for k, x := range v {
			out[k] = s.expand(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = s.expand(x)
		}
		return out
	default:
		return value
	}
}

func (s *Server) ragProvider() any {
	if s.retrieval.embedder == nil {
		return nil
	}
	provider, _ := s.retrieval.embedder.Describe()
	return provider
}
func (s *Server) ragModel() any {
	if s.retrieval.embedder == nil {
		return nil
	}
	_, model := s.retrieval.embedder.Describe()
	return model
}
