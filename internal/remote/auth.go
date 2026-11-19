package remote

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const RecordsScope = "facet:records:read"
const CodeScope = "facet:code:read"
const SearchScope = "facet:search:read"

type Principal struct {
	ID        string
	Scopes    []string
	ExpiresAt time.Time
	OAuth     bool
}
type Key struct {
	ID         string    `json:"id"`
	Digest     string    `json:"sha256"`
	Scopes     []string  `json:"scopes"`
	Transports []string  `json:"transports"`
	ExpiresAt  time.Time `json:"expires_at"`
	Disabled   bool      `json:"disabled,omitempty"`
}
type TokenProvider interface {
	Verify(context.Context, string) (Principal, error)
}
type Authenticator struct {
	Keys    []Key
	OAuth   TokenProvider
	MCPKeys bool
}

func GenerateKey(scopes, transports []string, expires time.Time) (string, Key, error) {
	var random [40]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", Key{}, err
	}
	id := hex.EncodeToString(random[:8])
	secret := "facet_k_" + id + "_" + base64.RawURLEncoding.EncodeToString(random[8:])
	hash := sha256.Sum256([]byte(secret))
	return secret, Key{ID: id, Digest: hex.EncodeToString(hash[:]), Scopes: scopes, Transports: transports, ExpiresAt: expires}, nil
}
func (a *Authenticator) Validate() error {
	seen := map[string]bool{}
	for _, k := range a.Keys {
		id, err := hex.DecodeString(k.ID)
		if err != nil || len(id) != 8 || strings.ToLower(k.ID) != k.ID || seen[k.ID] {
			return errors.New("invalid or duplicate API key id")
		}
		seen[k.ID] = true
		digest, err := hex.DecodeString(k.Digest)
		if err != nil || len(digest) != 32 || k.ExpiresAt.IsZero() || len(k.Scopes) == 0 || len(k.Transports) == 0 {
			return errors.New("invalid API key configuration")
		}
		for _, scope := range k.Scopes {
			if !slices.Contains([]string{RecordsScope, CodeScope, SearchScope}, scope) {
				return errors.New("invalid key permission")
			}
		}
		for _, transport := range k.Transports {
			if transport != "rest" && transport != "mcp" {
				return errors.New("invalid key transport")
			}
		}
	}
	if len(a.Keys) == 0 && a.OAuth == nil {
		return errors.New("authentication must be configured")
	}
	return nil
}
func (a *Authenticator) Verify(ctx context.Context, token, transport string) (Principal, error) {
	if len(token) == 0 || len(token) > 8192 {
		return Principal{}, problem(401, "invalid_token")
	}
	if strings.HasPrefix(token, "facet_k_") {
		if transport == "mcp" && !a.MCPKeys {
			return Principal{}, problem(401, "invalid_token")
		}
		if len(token) != 68 || token[24] != '_' {
			return Principal{}, problem(401, "invalid_token")
		}
		id := token[8:24]
		hash := sha256.Sum256([]byte(token))
		for _, k := range a.Keys {
			if k.ID != id {
				continue
			}
			digest, err := hex.DecodeString(k.Digest)
			if err != nil || subtle.ConstantTimeCompare(hash[:], digest) != 1 || k.Disabled || !time.Now().Before(k.ExpiresAt) || !slices.Contains(k.Transports, transport) {
				break
			}
			return Principal{ID: "key:" + id, Scopes: append([]string(nil), k.Scopes...), ExpiresAt: k.ExpiresAt}, nil
		}
		return Principal{}, problem(401, "invalid_token")
	}
	if transport != "mcp" || a.OAuth == nil {
		return Principal{}, problem(401, "invalid_token")
	}
	return a.OAuth.Verify(ctx, token)
}

// OAuthConfig is operator-owned. A subject allowlist is mandatory and independent
// of the OAuth scopes: registration or consent is not repository entitlement.
type OAuthConfig struct {
	Mode             string            `json:"mode"`
	Issuer           string            `json:"issuer"`
	Audience         string            `json:"audience"`
	JWKSURL          string            `json:"jwks_url,omitempty"`
	IntrospectionURL string            `json:"introspection_url,omitempty"`
	ClientID         string            `json:"client_id,omitempty"`
	ClientSecret     string            `json:"-"`
	Subjects         []string          `json:"subjects"`
	ScopeClaim       string            `json:"scope_claim"`
	ScopeMap         map[string]string `json:"scope_map"`
}
type OAuthProvider struct {
	config            OAuthConfig
	client            *http.Client
	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	expires           time.Time
	refreshed         time.Time
	lastRefreshFailed bool
	refreshing        chan struct{}
	introspection     chan struct{}
}

func NewOAuthProvider(cfg OAuthConfig) (*OAuthProvider, error) {
	if !httpsURL(cfg.Issuer) || cfg.Audience == "" || len(cfg.Subjects) == 0 || len(cfg.ScopeMap) == 0 {
		return nil, errors.New("OAuth requires HTTPS issuer, audience, subjects and scope mapping")
	}
	if cfg.ScopeClaim == "" {
		cfg.ScopeClaim = "scope"
	}
	if cfg.ScopeClaim != "scope" && cfg.ScopeClaim != "scp" {
		return nil, errors.New("unsupported OAuth scope claim")
	}
	for _, s := range cfg.Subjects {
		if s == "" || s == "*" {
			return nil, errors.New("explicit OAuth subjects required")
		}
	}
	for from, to := range cfg.ScopeMap {
		if from == "" || strings.ContainsAny(from, " \r\n\t") || !slices.Contains([]string{RecordsScope, CodeScope, SearchScope}, to) {
			return nil, errors.New("invalid OAuth scope mapping")
		}
	}
	switch cfg.Mode {
	case "jwt":
		if !httpsURL(cfg.JWKSURL) {
			return nil, errors.New("HTTPS JWKS URL required")
		}
	case "introspection":
		if !httpsURL(cfg.IntrospectionURL) || cfg.ClientID == "" || cfg.ClientSecret == "" {
			return nil, errors.New("authenticated HTTPS introspection required")
		}
	default:
		return nil, errors.New("unsupported OAuth validation mode")
	}
	return &OAuthProvider{config: cfg, client: &http.Client{Transport: trustedTransport(), Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}, keys: map[string]*rsa.PublicKey{}, introspection: make(chan struct{}, 4)}, nil
}
func httpsURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == "" && u.RawQuery == ""
}
func (p *OAuthProvider) Verify(ctx context.Context, token string) (Principal, error) {
	if strings.HasPrefix(token, "facet_k_") || len(token) == 0 || len(token) > 8192 {
		return Principal{}, problem(401, "invalid_token")
	}
	if p.config.Mode == "introspection" {
		return p.introspect(ctx, token)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if t.Header["typ"] != "at+jwt" && t.Header["typ"] != "application/at+jwt" {
			return nil, problem(401, "invalid_token")
		}
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, problem(401, "invalid_token")
		}
		return p.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(p.config.Issuer), jwt.WithAudience(p.config.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil || parsed == nil || !parsed.Valid {
		var failure *Problem
		if errors.As(err, &failure) && failure.Status == 503 {
			return Principal{}, failure
		}
		return Principal{}, problem(401, "invalid_token")
	}
	if _, ok := claims["iat"]; !ok {
		return Principal{}, problem(401, "invalid_token")
	}
	if id, ok := claims["jti"].(string); !ok || id == "" {
		return Principal{}, problem(401, "invalid_token")
	}
	if client, ok := claims["client_id"].(string); !ok || client == "" {
		return Principal{}, problem(401, "invalid_token")
	}
	return p.principal(claims)
}
func (p *OAuthProvider) principal(claims jwt.MapClaims) (Principal, error) {
	sub, err := claims.GetSubject()
	if err != nil || !slices.Contains(p.config.Subjects, sub) {
		return Principal{}, problem(401, "invalid_token")
	}
	aud, err := claims.GetAudience()
	if err != nil || len(aud) != 1 || aud[0] != p.config.Audience {
		return Principal{}, problem(401, "invalid_token")
	}
	expiry, err := claims.GetExpirationTime()
	if err != nil || expiry == nil || !time.Now().Before(expiry.Time) {
		return Principal{}, problem(401, "invalid_token")
	}
	raw, ok := claims[p.config.ScopeClaim].(string)
	if !ok {
		return Principal{}, problem(401, "invalid_token")
	}
	scopes := []string{}
	for _, s := range strings.Fields(raw) {
		if mapped := p.config.ScopeMap[s]; mapped != "" && !slices.Contains(scopes, mapped) {
			scopes = append(scopes, mapped)
		}
	}
	client, _ := claims["client_id"].(string)
	identity := sha256.Sum256([]byte(p.config.Issuer + "\x00" + sub + "\x00" + client))
	return Principal{ID: "oauth:" + hex.EncodeToString(identity[:]), Scopes: scopes, ExpiresAt: expiry.Time, OAuth: true}, nil
}
func (p *OAuthProvider) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	now := time.Now()
	if now.Before(p.expires) {
		if key := p.keys[kid]; key != nil {
			p.mu.Unlock()
			return key, nil
		}
	}
	if wait := p.refreshing; wait != nil {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, problem(503, "auth_provider_unavailable")
		case <-wait:
			return p.key(ctx, kid)
		}
	}
	if now.Sub(p.refreshed) < 5*time.Second {
		failed := p.lastRefreshFailed
		p.mu.Unlock()
		if failed {
			return nil, problem(503, "auth_provider_unavailable")
		}
		return nil, problem(401, "invalid_token")
	}
	p.refreshed = now
	p.lastRefreshFailed = true
	p.refreshing = make(chan struct{})
	p.mu.Unlock()
	// One request cannot cancel a shared trusted refresh. Cached hits never wait on IO.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	defer func() { p.mu.Lock(); close(p.refreshing); p.refreshing = nil; p.mu.Unlock() }()
	return p.refreshKeys(refreshCtx, kid, now)
}
func (p *OAuthProvider) refreshKeys(ctx context.Context, kid string, now time.Time) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.JWKSURL, nil)
	if err != nil {
		return nil, problem(503, "auth_provider_unavailable")
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, problem(503, "auth_provider_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, problem(503, "auth_provider_unavailable")
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Alg string `json:"alg"`
			Use string `json:"use"`
		} `json:"keys"`
	}
	if decodeBounded(res.Body, &doc) != nil || len(doc.Keys) > 100 {
		return nil, problem(503, "auth_provider_unavailable")
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range doc.Keys {
		if j.Kty != "RSA" || (j.Alg != "" && j.Alg != "RS256") || (j.Use != "" && j.Use != "sig") || j.Kid == "" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(j.N)
		eb, e2 := base64.RawURLEncoding.DecodeString(j.E)
		if e1 != nil || e2 != nil || len(eb) > 4 {
			continue
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(eb).Int64()
		if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent < 3 || exponent%2 == 0 {
			continue
		}
		if _, duplicate := keys[j.Kid]; duplicate {
			return nil, problem(503, "auth_provider_unavailable")
		}
		keys[j.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent)}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = keys
	p.expires = now.Add(5 * time.Minute)
	p.lastRefreshFailed = false
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, problem(401, "invalid_token")
}
func (p *OAuthProvider) introspect(ctx context.Context, token string) (Principal, error) {
	select {
	case p.introspection <- struct{}{}:
		defer func() { <-p.introspection }()
	default:
		return Principal{}, problem(429, "concurrency_limited")
	}
	body := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.IntrospectionURL, strings.NewReader(body.Encode()))
	if err != nil {
		return Principal{}, problem(503, "auth_provider_unavailable")
	}
	req.SetBasicAuth(p.config.ClientID, p.config.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.client.Do(req)
	if err != nil {
		return Principal{}, problem(503, "auth_provider_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Principal{}, problem(503, "auth_provider_unavailable")
	}
	claims := jwt.MapClaims{}
	if decodeBounded(res.Body, &claims) != nil {
		return Principal{}, problem(503, "auth_provider_unavailable")
	}
	if claims["active"] != true {
		return Principal{}, problem(401, "invalid_token")
	}
	if issuer, ok := claims["iss"]; ok && issuer != p.config.Issuer {
		return Principal{}, problem(401, "invalid_token")
	}
	if nbf, err := claims.GetNotBefore(); err != nil || (nbf != nil && time.Now().Add(30*time.Second).Before(nbf.Time)) {
		return Principal{}, problem(401, "invalid_token")
	}
	return p.principal(claims)
}
func decodeBounded(r io.Reader, target any) error {
	b, e := io.ReadAll(io.LimitReader(r, 65537))
	if e != nil || len(b) > 65536 {
		return errors.New("provider response too large")
	}
	if _, err := strictJSON(b); err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
