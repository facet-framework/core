package rag

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/facet-framework/core/registry"
)

// DefaultProvider is what a project gets when it configures nothing.
const DefaultProvider = "local"

// EgressClass names the repository-derived data a remote embedder will see.
// Separate values keep approval for indexing from silently authorizing queries.
type EgressClass string

const (
	RecordEgress EgressClass = "record"
	QueryEgress  EgressClass = "query"
)

// RemoteOptions carries authority that must come from the current invocation,
// never from repository configuration. ApprovedOrigin is an exact HTTPS origin
// such as https://api.example.com. APIKeyEnv names a credential selected by the
// invoker and is read only after the destination and data class are approved.
type RemoteOptions struct {
	Egress         EgressClass
	ApprovedOrigin string
	APIKeyEnv      string
}

// Resolve turns configuration into an embedder.
func Resolve(cfg facet.EmbeddingConfig, authority ...RemoteOptions) (Embedder, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = DefaultProvider
	}

	switch provider {
	case "local":
		return &LocalEmbedder{Dim: cfg.Dimensions}, nil

	case "openai", "openai-compatible", "ollama", "lmstudio", "http":
		base := strings.TrimSpace(cfg.BaseURL)
		if base == "" {
			return nil, fmt.Errorf(
				"embedding.provider is %q but no embedding.base_url is set in %s — "+
					"give the HTTPS API root without /embeddings, for example https://api.openai.com/v1",
				cfg.Provider, facet.ConfigName)
		}
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, fmt.Errorf("embedding.provider is %q but no embedding.model is set in %s",
				cfg.Provider, facet.ConfigName)
		}
		if cfg.APIKeyEnv != "" {
			return nil, fmt.Errorf("remote embedding credentials must be selected by the current invocation, not embedding configuration")
		}

		origin, err := embeddingOrigin(base, false)
		if err != nil {
			return nil, err
		}
		if len(authority) != 1 {
			return nil, fmt.Errorf("remote embedding to %s requires invocation-time approval for the data being sent", origin)
		}
		approved := authority[0]
		data, flag, err := egressDescription(approved.Egress)
		if err != nil {
			return nil, err
		}
		approvedOrigin, err := embeddingOrigin(approved.ApprovedOrigin, true)
		if err != nil || approvedOrigin != origin {
			return nil, fmt.Errorf(
				"remote embedding would send %s to %s; pass %s=%s to approve this exact HTTPS origin for this invocation",
				data, origin, flag, origin)
		}

		// The repository cannot name this variable. The selector arrives in
		// RemoteOptions and the secret is read only after exact-origin approval.
		// Empty remains valid for HTTPS providers that require no credential.
		key := ""
		if approved.APIKeyEnv != "" {
			key = os.Getenv(approved.APIKeyEnv)
		}

		return &HTTPEmbedder{
			BaseURL:    base,
			Model:      cfg.Model,
			APIKey:     key,
			Dimensions: cfg.Dimensions,
			Batch:      cfg.Batch,
			Client:     embeddingHTTPClient(),
		}, nil

	default:
		return nil, fmt.Errorf("unknown embedding.provider %q — use \"openai\" for any OpenAI-compatible /embeddings endpoint, or \"local\" for the built-in lexical embedder", cfg.Provider)
	}
}

func egressDescription(class EgressClass) (data, flag string, err error) {
	switch class {
	case RecordEgress:
		return "repository record and generated-document text", "--allow-record-egress", nil
	case QueryEgress:
		return "query text", "--allow-query-egress", nil
	default:
		return "", "", fmt.Errorf("remote embedding requires an explicit record or query egress class")
	}
}

// embeddingOrigin validates the network boundary and returns its canonical
// origin. An approval is origin-only; an endpoint may additionally carry a
// path such as /v1, but never credentials, a query, or a fragment.
func embeddingOrigin(raw string, approval bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" || u.Host == "" || !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("remote embedding requires an HTTPS endpoint")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("remote embedding endpoint must not contain credentials, a query, or a fragment")
	}
	if approval && u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("remote embedding approval must name an HTTPS origin without a path")
	}

	hostname := strings.ToLower(u.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("remote embedding requires an HTTPS endpoint")
	}
	port := u.Port()
	if port == "443" {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return "https://" + host, nil
}

// embeddingHTTPClient refuses redirects and deliberately has no ambient
// proxy. Approval therefore remains bound to the origin that Resolve checked.
func embeddingHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 16 << 10
	return &http.Client{
		Transport: transport,
		Timeout:   DefaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("rag: embedding redirects are forbidden")
		},
	}
}

// Describe renders an embedder's identity for a person to read.
func Describe(e Embedder) string {
	provider, model := e.Describe()
	return provider + "/" + model
}
