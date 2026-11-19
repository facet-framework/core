package remote

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJWKSRefreshSurvivesCallerCancellationAndRotates(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	var calls atomic.Int32
	var kid atomic.Value
	kid.Store("one")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": kid.Load(), "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer server.Close()
	p, err := NewOAuthProvider(OAuthConfig{Mode: "jwt", Issuer: server.URL, Audience: "https://facet.example/mcp", JWKSURL: server.URL, Subjects: []string{"user"}, ScopeMap: map[string]string{"read": RecordsScope}})
	if err != nil {
		t.Fatal(err)
	}
	p.client = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.key(ctx, "one"); err != nil {
		t.Fatal("caller cancelled shared refresh", err)
	}
	if _, err := p.key(context.Background(), "absent"); asProblem(err).Status != 401 {
		t.Fatal("unknown key response", err)
	}
	if calls.Load() != 1 {
		t.Fatal("refresh not throttled")
	}
	kid.Store("two")
	p.expires = time.Time{}
	p.refreshed = time.Time{}
	if _, err := p.key(context.Background(), "two"); err != nil {
		t.Fatal("rotation", err)
	}
	fail.Store(true)
	p.expires = time.Time{}
	p.refreshed = time.Time{}
	if _, err := p.key(context.Background(), "two"); asProblem(err).Code != "auth_provider_unavailable" {
		t.Fatal("outage not classified", err)
	}
}
func TestIntrospectionNegativeClaimsAndOutage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		patch  map[string]any
		status int
		want   int
	}{{"inactive", map[string]any{"active": false}, 200, 401}, {"audience", map[string]any{"aud": "wrong"}, 200, 401}, {"multiple audiences", map[string]any{"aud": []string{"https://facet.example/mcp", "extra"}}, 200, 401}, {"expiry", map[string]any{"exp": nil}, 200, 401}, {"subject", map[string]any{"sub": "stranger"}, 200, 401}, {"issuer", map[string]any{"iss": "https://wrong.example"}, 200, 401}, {"future", map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}, 200, 401}, {"outage", nil, 503, 503}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				claims := map[string]any{"active": true, "sub": "allowed", "aud": "https://facet.example/mcp", "exp": time.Now().Add(time.Hour).Unix(), "scope": "read"}
				for k, v := range tc.patch {
					claims[k] = v
				}
				json.NewEncoder(w).Encode(claims)
			}))
			defer server.Close()
			p, err := NewOAuthProvider(OAuthConfig{Mode: "introspection", Issuer: server.URL, Audience: "https://facet.example/mcp", IntrospectionURL: server.URL, ClientID: "client", ClientSecret: "secret", Subjects: []string{"allowed"}, ScopeMap: map[string]string{"read": RecordsScope}})
			if err != nil {
				t.Fatal(err)
			}
			p.client = server.Client()
			_, err = p.Verify(context.Background(), "opaque")
			var problem *Problem
			if !errors.As(err, &problem) || problem.Status != tc.want {
				t.Fatal(err)
			}
		})
	}
}
func TestIntrospectionConcurrencyAdmission(t *testing.T) {
	p, err := NewOAuthProvider(OAuthConfig{Mode: "introspection", Issuer: "https://issuer.example", Audience: "https://facet.example/mcp", IntrospectionURL: "https://issuer.example/check", ClientID: "client", ClientSecret: "secret", Subjects: []string{"allowed"}, ScopeMap: map[string]string{"read": RecordsScope}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		p.introspection <- struct{}{}
	}
	defer func() {
		for i := 0; i < 4; i++ {
			<-p.introspection
		}
	}()
	if _, err := p.Verify(context.Background(), "opaque"); asProblem(err).Status != 429 {
		t.Fatal("unbounded introspection", err)
	}
}
func TestSectionsAreOriginalBytesAndScalarsCannotCreateSections(t *testing.T) {
	raw := "# comment\r\nid: navigation/interfaces/example\r\nintent: |\r\n  not_a_child: scalar text\r\ncontract:\r\n  scenarios: []"
	sections, err := splitRecordSections(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range sections {
		if strings.Contains(section.Name, "not_a_child") {
			t.Fatal("scalar became section")
		}
	}
	r := Record{YAML: raw, sections: sections}
	s, ok := r.section("intent")
	if !ok || raw[s.Start:s.End] != "intent: |\r\n  not_a_child: scalar text\r\n" {
		t.Fatalf("bytes changed: %#v %q all=%#v", s, raw[s.Start:s.End], sections)
	}
	corpus := &Corpus{records: []Record{{ID: "navigation/interfaces/example", Category: "features", YAML: raw, sections: sections}}}
	out, err := NewReader(corpus).sections(Input{ID: "navigation/interfaces/example", Sections: []string{"contract.scenarios"}})
	if err != nil {
		t.Fatal(err)
	}
	v := out.Sections[0]
	if v.Bytes != len(*v.Text) || *v.Text != "  scenarios: []" || v.Lines != 1 {
		t.Fatal("section counters disagree", v)
	}
}
func TestCorpusNeverUsesAmbientConfig(t *testing.T) {
	root, pub := fixture(t)
	t.Setenv("FACET_RECORDS_DIR", "/etc")
	t.Setenv("FACET_READ_GATE_BYTES", "1")
	if err := os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("records: /etc\nplugins: [malicious]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCorpus(root, pub)
	if err != nil || len(c.records) != 1 {
		t.Fatal("ambient configuration changed corpus", err)
	}
	for _, name := range []string{"id_rsa", "keys/id_ed25519", "a.crt", "a.keystore", "a.tfstate", "credentials.json", "kubeconfig"} {
		if validPath(name) {
			t.Fatal("secret path accepted", name)
		}
	}
	for _, raw := range []string{`{"schema_version":1,"schema_version":2,"files":[]}`, `{"schema_version":1,"files":[]}` + strings.Repeat(" ", 1<<20)} {
		if _, err := LoadPublication(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid publication accepted")
		}
	}
}
func TestAuditAndHeaderBoundary(t *testing.T) {
	s, key := serverFixture(t)
	var event AuditEvent
	s.config.Audit = func(e AuditEvent) { event = e }
	request(t, s, key, "GET", "/v1/records?query=PRIVATE_QUERY", "")
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "PRIVATE_QUERY") || event.Operation != "records_find" || event.Status != 200 || event.Principal == "" {
		t.Fatal("audit contract", string(encoded))
	}
	req := httptest.NewRequest("GET", "https://facet.example/v1/records", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Padding", strings.Repeat("x", 16384))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 431 {
		t.Fatal("header limit", w.Code)
	}
}

func TestSectionsRejectBareCarriageReturn(t *testing.T) {
	for _, raw := range []string{"a: \"x\ry\"\nb: 1\nc:\n  d: 1\n  e: 2\n", "a: 1\rb: 2\n"} {
		if _, err := splitRecordSections(raw); err == nil {
			t.Fatal("accepted ambiguous carriage return")
		}
	}
}

func TestMCPToolFailureAudit(t *testing.T) {
	s, key := serverFixture(t)
	var event AuditEvent
	s.config.Audit = func(e AuditEvent) { event = e }
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"record_get","arguments":{"id":"missing/record/id"}}}`
	req := httptest.NewRequest("POST", "https://facet.example/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if event.Status != 200 || event.Code != "not_found" || strings.Contains(w.Body.String(), "structuredContent") || !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(event, w.Body.String())
	}
}
