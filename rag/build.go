package rag

import (
	"context"
	"fmt"
	"time"

	"github.com/facet-framework/core/registry"
)

// Progress reports one step of a build. It is called from the goroutine doing
// the work, so a slow implementation slows the build.
type Progress func(phase string, done, total int)

// Build reads every registry into chunks, embeds them, and returns a store.
//
// @facet: navigation/retrieval/registry-index
// @facet-role: related
func Build(ctx context.Context, registries []facet.Registry, embedder Embedder, report Progress) (*Store, error) {
	if report == nil {
		report = func(string, int, int) {}
	}
	multi := len(registries) > 1

	var chunks []Chunk
	for _, reg := range registries {
		found, err := Chunks(reg, multi)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, found...)
		report("read", len(chunks), 0)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("rag: no records and no generated documents to index — run 'facet init' to set up a registry, then write its first record, or point --root at the repository that has them")
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	// Embedded in slices rather than one call, so progress is real and a very
	// large registry does not sit silent for a minute. The embedder batches
	// again inside this; the two are independent on purpose, because the batch
	// that a gateway accepts and the batch that makes a progress bar move are
	// not the same number.
	const step = 128
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += step {
		end := start + step
		if end > len(texts) {
			end = len(texts)
		}
		part, err := embedder.Embed(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, part...)
		report("embed", len(vectors), len(texts))
	}

	provider, model := embedder.Describe()
	return NewStore(provider, model, time.Now().UTC().Format(time.RFC3339), chunks, vectors)
}

// Query embeds one question and searches a store.
func Query(ctx context.Context, store *Store, embedder Embedder, question string, k int) ([]Hit, error) {
	provider, model := embedder.Describe()
	if store.Model != model || store.Provider != provider {
		return nil, fmt.Errorf(
			"rag: the index was built with %s/%s and this query would use %s/%s — "+
				"vectors from two models are not comparable; rebuild with 'facet rag index' or query with the model that built it",
			store.Provider, store.Model, provider, model)
	}

	vectors, err := embedder.Embed(ctx, []string{question})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("rag: the embedding provider returned %d vector(s) for one question", len(vectors))
	}
	return store.Search(vectors[0], k)
}
