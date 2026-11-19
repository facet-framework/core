package remote

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type spyProvider struct{ calls int }

func (p *spyProvider) Verify(context.Context, string) (Principal, error) {
	p.calls++
	return Principal{}, problem(401, "invalid_token")
}
func TestKeyCredentialRoutingAndRevocation(t *testing.T) {
	token, key, err := GenerateKey([]string{RecordsScope}, []string{"rest", "mcp"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	spy := &spyProvider{}
	a := Authenticator{Keys: []Key{key}, OAuth: spy, MCPKeys: true}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(context.Background(), token, "rest"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"facet_k_invalid", token + "x"} {
		if _, err := a.Verify(context.Background(), bad, "mcp"); err == nil {
			t.Fatal("accepted invalid key")
		}
	}
	if spy.calls != 0 {
		t.Fatal("API key leaked to OAuth provider")
	}
	a.Keys[0].Disabled = true
	if _, err := a.Verify(context.Background(), token, "rest"); err == nil {
		t.Fatal("disabled key accepted")
	}
	a.Keys[0].Disabled = false
	a.Keys[0].ExpiresAt = time.Now().Add(-time.Second)
	if _, err := a.Verify(context.Background(), token, "rest"); err == nil {
		t.Fatal("expired key accepted")
	}
	a.Keys[0] = key
	a.Keys[0].Transports = []string{"rest"}
	if _, err := a.Verify(context.Background(), token, "mcp"); err == nil {
		t.Fatal("transport binding ignored")
	}
}
func TestJWTValidationAndEntitlement(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "one", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}, map[string]any{"kty": "RSA", "kid": "weak", "n": base64.RawURLEncoding.EncodeToString(weak.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(weak.E)).Bytes())}}})
	}))
	defer server.Close()
	cfg := OAuthConfig{Mode: "jwt", Issuer: server.URL, Audience: "https://facet.example/mcp", JWKSURL: server.URL, Subjects: []string{"allowed"}, ScopeMap: map[string]string{"read": RecordsScope}}
	provider, err := NewOAuthProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	provider.client = server.Client()
	provider.client.Timeout = time.Second
	claims := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": server.URL, "aud": cfg.Audience, "sub": "allowed", "jti": "fixture-token", "client_id": "client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "scope": "read"}
	}
	sign := func(c jwt.MapClaims, typ string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		token.Header["kid"] = "one"
		token.Header["typ"] = typ
		value, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	good := sign(claims(), "at+jwt")
	p, err := provider.Verify(context.Background(), good)
	if err != nil || len(p.Scopes) != 1 {
		t.Fatalf("valid JWT denied %+v %v", p, err)
	}
	for _, mutate := range []func(jwt.MapClaims){func(c jwt.MapClaims) { c["aud"] = "wrong" }, func(c jwt.MapClaims) { c["iss"] = "https://wrong" }, func(c jwt.MapClaims) { c["sub"] = "other" }, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, func(c jwt.MapClaims) { delete(c, "client_id") }, func(c jwt.MapClaims) { delete(c, "jti") }, func(c jwt.MapClaims) { delete(c, "iat") }, func(c jwt.MapClaims) { c["aud"] = []string{cfg.Audience, "other"} }} {
		c := claims()
		mutate(c)
		if _, err := provider.Verify(context.Background(), sign(c, "at+jwt")); err == nil {
			t.Fatalf("accepted invalid claims %+v", c)
		}
	}
	if _, err := provider.Verify(context.Background(), sign(claims(), "JWT")); err == nil {
		t.Fatal("accepted ID-token type")
	}

	if _, err := provider.Verify(context.Background(), sign(claims(), "application/at+jwt")); err != nil {
		t.Fatal("registered JWT media type rejected", err)
	}
	for _, tc := range []struct {
		method jwt.SigningMethod
		key    any
		kid    string
	}{
		{jwt.SigningMethodHS256, []byte("fixture-hmac-secret-long-enough-for-test"), "one"},
		{jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "one"},
		{jwt.SigningMethodRS256, weak, "weak"},
	} {
		token := jwt.NewWithClaims(tc.method, claims())
		token.Header["typ"], token.Header["kid"] = "at+jwt", tc.kid
		value, err := token.SignedString(tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Verify(context.Background(), value); err == nil {
			t.Fatal("accepted disallowed algorithm or weak key", tc.method.Alg(), tc.kid)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unbounded JWKS requests: %d", calls.Load())
	}
}
func TestIntrospectionNeverReceivesAPIKeys(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		user, secret, ok := r.BasicAuth()
		if !ok || user != "client" || secret != "secret" {
			t.Error("missing client authentication")
		}
		json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": "allowed", "aud": "https://facet.example/mcp", "exp": time.Now().Add(time.Hour).Unix(), "scope": "read"})
	}))
	defer server.Close()
	provider, err := NewOAuthProvider(OAuthConfig{Mode: "introspection", Issuer: server.URL, Audience: "https://facet.example/mcp", IntrospectionURL: server.URL, ClientID: "client", ClientSecret: "secret", Subjects: []string{"allowed"}, ScopeMap: map[string]string{"read": RecordsScope}})
	if err != nil {
		t.Fatal(err)
	}
	provider.client = server.Client()
	if _, err := provider.Verify(context.Background(), "facet_k_not-a-token"); err == nil {
		t.Fatal("accepted key")
	}
	if calls.Load() != 0 {
		t.Fatal("API key sent to provider")
	}
	if _, err := provider.Verify(context.Background(), "opaque-token"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("introspection missing")
	}
}
