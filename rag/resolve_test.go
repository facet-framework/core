package rag

import (
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

func TestResolveBuildsAnHTTPEmbedderAndReadsTheKeyFromTheNamedVariable(t *testing.T) {
	t.Setenv("MY_EMBEDDING_KEY", "sk-not-a-real-key")

	e, err := Resolve(facet.EmbeddingConfig{
		Provider:  "openai",
		BaseURL:   "https://api.example.com/v1",
		Model:     "text-embedding-3-small",
		APIKeyEnv: "MY_EMBEDDING_KEY",
		Batch:     8,
	})
	if err != nil {
		t.Fatal(err)
	}
	http, ok := e.(*HTTPEmbedder)
	if !ok {
		t.Fatalf("want an HTTP embedder, got %T", e)
	}
	if http.APIKey != "sk-not-a-real-key" {
		t.Fatal("the key must be read from the variable the configuration names")
	}
	if http.Batch != 8 || http.Model != "text-embedding-3-small" {
		t.Fatalf("configuration was not carried through: %+v", http)
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

// A missing key is not an error at resolve time: a local Ollama needs none, and
// a hosted provider that does will say so in its own words, which are clearer
// than a guess made here.
func TestResolveToleratesAnUnsetKeyVariable(t *testing.T) {
	if _, err := Resolve(facet.EmbeddingConfig{
		Provider: "openai", BaseURL: "http://localhost:11434/v1",
		Model: "nomic-embed-text", APIKeyEnv: "DEFINITELY_NOT_SET_ANYWHERE",
	}); err != nil {
		t.Fatalf("a local endpoint with no key must resolve: %v", err)
	}
}
