package rag

import (
	"net/http"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestResolveDefaultsToTheLocalEmbedder(t *testing.T) {
	e, err := Resolve(facet.EmbeddingConfig{})
	if err != nil {
		t.Fatalf("a project that configures nothing must still be able to index: %v", err)
	}
	if _, ok := e.(*LocalEmbedder); !ok {
		t.Fatalf("want the local embedder, got %T", e)
	}
	if Describe(e) != "local/facet-lexical-v1" {
		t.Fatalf("the built-in embedder must be visibly the built-in one, got %s", Describe(e))
	}
}

func TestResolveBuildsAnApprovedHTTPEmbedderAndReadsTheInvocationKey(t *testing.T) {
	t.Setenv("MY_EMBEDDING_KEY", "sk-not-a-real-key")

	e, err := Resolve(facet.EmbeddingConfig{
		Provider: "openai",
		BaseURL:  "https://api.example.com/v1",
		Model:    "text-embedding-3-small",
		Batch:    8,
	}, RemoteOptions{
		Egress:         RecordEgress,
		ApprovedOrigin: "https://api.example.com",
		APIKeyEnv:      "MY_EMBEDDING_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	remote, ok := e.(*HTTPEmbedder)
	if !ok {
		t.Fatalf("want an HTTP embedder, got %T", e)
	}
	if remote.APIKey != "sk-not-a-real-key" {
		t.Fatal("the key must be read from the variable the invocation names")
	}
	if remote.Batch != 8 || remote.Model != "text-embedding-3-small" {
		t.Fatalf("configuration was not carried through: %+v", remote)
	}
	transport, ok := remote.Client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("remote embeddings must ignore ambient proxies: %#v", remote.Client.Transport)
	}
	if remote.Client.CheckRedirect == nil {
		t.Fatal("remote embeddings must refuse redirects away from the approved origin")
	}
}

func TestResolveRequiresDataSpecificExactOriginApproval(t *testing.T) {
	cfg := facet.EmbeddingConfig{
		Provider: "openai",
		BaseURL:  "https://api.example.com/v1",
		Model:    "text-embedding-3-small",
	}
	for _, tc := range []struct {
		name  string
		class EgressClass
		text  string
		flag  string
	}{
		{"records", RecordEgress, "repository record and generated-document text", "--allow-record-egress"},
		{"query", QueryEgress, "query text", "--allow-query-egress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(cfg, RemoteOptions{Egress: tc.class})
			if err == nil || !strings.Contains(err.Error(), tc.text) || !strings.Contains(err.Error(), tc.flag+"=https://api.example.com") {
				t.Fatalf("missing approval must name the data and exact-origin flag, got %v", err)
			}
		})
	}

	_, err := Resolve(cfg, RemoteOptions{Egress: QueryEgress, ApprovedOrigin: "https://attacker.example"})
	if err == nil || !strings.Contains(err.Error(), "https://api.example.com") {
		t.Fatalf("approval for another origin must not authorize this endpoint, got %v", err)
	}
}

func TestResolveRejectsLegacyConfigCredentialSelector(t *testing.T) {
	t.Setenv("AMBIENT_DEPLOYMENT_SECRET", "must-not-be-read")
	_, err := Resolve(facet.EmbeddingConfig{
		Provider: "openai", BaseURL: "https://api.example.com/v1", Model: "m",
		APIKeyEnv: "AMBIENT_DEPLOYMENT_SECRET",
	}, RemoteOptions{Egress: QueryEgress, ApprovedOrigin: "https://api.example.com"})
	if err == nil || !strings.Contains(err.Error(), "current invocation") {
		t.Fatalf("legacy configuration must be rejected before selecting an ambient credential, got %v", err)
	}
}

func TestResolveRejectsUnsafeEndpointForms(t *testing.T) {
	for _, endpoint := range []string{
		"http://api.example.com/v1",
		"https://token@api.example.com/v1",
		"https://api.example.com/v1?destination=other",
		"https://api.example.com/v1#other",
	} {
		_, err := Resolve(facet.EmbeddingConfig{Provider: "openai", BaseURL: endpoint, Model: "m"}, RemoteOptions{
			Egress: QueryEgress, ApprovedOrigin: "https://api.example.com",
		})
		if err == nil {
			t.Fatalf("unsafe endpoint %q must be rejected", endpoint)
		}
	}
}

func TestResolveSaysWhatIsMissing(t *testing.T) {
	_, err := Resolve(facet.EmbeddingConfig{Provider: "openai", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("a provider with no endpoint must say so, got %v", err)
	}

	_, err = Resolve(facet.EmbeddingConfig{Provider: "openai", BaseURL: "https://x/v1"})
	if err == nil || !strings.Contains(err.Error(), "embedding.model") {
		t.Fatalf("a provider with no model must say so, got %v", err)
	}

	_, err = Resolve(facet.EmbeddingConfig{Provider: "pinecone"})
	if err == nil || !strings.Contains(err.Error(), "pinecone") {
		t.Fatalf("an unknown provider must name itself in the error, got %v", err)
	}
}

// A missing key is not an error at resolve time: an approved HTTPS provider
// may need none, and one that does will return its own authentication error.
func TestResolveToleratesAnUnsetInvocationKey(t *testing.T) {
	if _, err := Resolve(facet.EmbeddingConfig{
		Provider: "openai", BaseURL: "https://embeddings.example/v1",
		Model: "nomic-embed-text",
	}, RemoteOptions{
		Egress: QueryEgress, ApprovedOrigin: "https://embeddings.example",
		APIKeyEnv: "DEFINITELY_NOT_SET_ANYWHERE",
	}); err != nil {
		t.Fatalf("an approved keyless endpoint must resolve: %v", err)
	}
}
