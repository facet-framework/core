package rag

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/facet-framework/core/registry"
)

// DefaultProvider is what a project gets when it configures nothing.
const DefaultProvider = "local"

// Resolve turns configuration into an embedder.
func Resolve(cfg facet.EmbeddingConfig) (Embedder, error) {
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
					"give the API root without /embeddings, for example https://api.openai.com/v1 "+
					"or http://localhost:11434/v1 for a local Ollama", cfg.Provider, facet.ConfigName)
		}
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, fmt.Errorf("embedding.provider is %q but no embedding.model is set in %s",
				cfg.Provider, facet.ConfigName)
		}

		// An unset variable is not an error here. A local endpoint needs no
		// key, and a hosted one that does fails with the provider's own
		// message, which is clearer than anything guessed at this distance.
		key := ""
		if cfg.APIKeyEnv != "" {
			key = os.Getenv(cfg.APIKeyEnv)
		}

		return &HTTPEmbedder{
			BaseURL:    base,
			Model:      cfg.Model,
			APIKey:     key,
			Dimensions: cfg.Dimensions,
			Batch:      cfg.Batch,
			Client:     &http.Client{Timeout: DefaultTimeout},
		}, nil

	default:
		return nil, fmt.Errorf("unknown embedding.provider %q — use \"openai\" for any OpenAI-compatible /embeddings endpoint, or \"local\" for the built-in lexical embedder", cfg.Provider)
	}
}

// Describe renders an embedder's identity for a person to read.
func Describe(e Embedder) string {
	provider, model := e.Describe()
	return provider + "/" + model
}
