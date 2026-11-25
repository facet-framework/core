package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/facet-framework/core/rag"
	"github.com/facet-framework/core/registry"
)

// The retrieval half of the Agent Interface.
//
// @facet: navigation/retrieval/semantic-search
// @facet-role: related

type ragQueryInput struct {
	Query string `json:"query" jsonschema:"a natural-language question, in the words you would ask a colleague"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many chunks to return, default 5, maximum 20"`
	Kind  string `json:"kind,omitempty" jsonschema:"restrict to \"record\" for FACET record text or \"doc\" for generated documentation"`
}

// ragHit is one result as the agent sees it.
type ragHit struct {
	Feature  string  `json:"feature,omitempty"`
	Registry string  `json:"registry,omitempty"`
	Kind     string  `json:"kind"`
	Facet    string  `json:"facet"`
	Path     string  `json:"path"`
	Score    float32 `json:"score"`
	Text     string  `json:"text"`
}

type ragQueryOutput struct {
	Hits []ragHit `json:"hits"`
	// Model says what embedded the index. An agent that sees "local" is being
	// told the retrieval it just did matched words, not meaning, which changes
	// how much weight a middling score deserves.
	Model  string `json:"model"`
	Chunks int    `json:"chunks_searched"`
}

// ragQuery loads the index and searches it.
func ragQuery(ctx context.Context, set facet.RegistrySet, in ragQueryInput, authority ...rag.RemoteOptions) (ragQueryOutput, error) {
	if in.Query == "" {
		return ragQueryOutput{}, fmt.Errorf("rag_query needs a question")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	reg := set.Primary()
	cfg, err := facet.LoadConfig(reg.Root)
	if err != nil {
		return ragQueryOutput{}, err
	}
	path := cfg.IndexPath(reg)

	store, err := rag.LoadStore(path)
	if os.IsNotExist(err) {
		return ragQueryOutput{}, fmt.Errorf(
			"no retrieval index at %s — build one with 'facet rag index'. Until then, use feature_find and feature_get",
			reg.Rel(path))
	}
	if err != nil {
		return ragQueryOutput{}, err
	}

	embedder, err := rag.Resolve(cfg.EmbeddingSettings(), authority...)
	if err != nil {
		return ragQueryOutput{}, err
	}

	hits, err := rag.Query(ctx, store, embedder, in.Query, limit)
	if err != nil {
		return ragQueryOutput{}, err
	}

	out := ragQueryOutput{Hits: []ragHit{}, Model: store.Provider + "/" + store.Model, Chunks: store.Len()}
	for _, h := range hits {
		if in.Kind != "" && h.Kind != in.Kind {
			continue
		}
		out.Hits = append(out.Hits, ragHit{
			Feature:  h.Feature,
			Registry: h.Registry,
			Kind:     h.Kind,
			Facet:    h.Facet,
			Path:     h.Path,
			Score:    h.Score,
			Text:     h.Text,
		})
	}
	return out, nil
}
