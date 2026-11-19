package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/facet-framework/core/rag"
)

// BuildIndex is an operator-only offline operation. It never runs from HTTP/MCP.
// Chunk bytes are exact section slices, unlike the enriched general-purpose index.
func BuildIndex(ctx context.Context, corpus *Corpus, cfg *RAGConfig, allowRecordEgress bool) (*rag.Store, RAGProvenance, error) {
	if cfg == nil {
		return nil, RAGProvenance{}, errors.New("RAG configuration required")
	}
	if cfg.Provider == "http" && !allowRecordEgress {
		return nil, RAGProvenance{}, errors.New("remote indexing requires explicit record egress approval")
	}
	retrieval := &Retrieval{}
	if err := retrieval.configure(cfg); err != nil {
		return nil, RAGProvenance{}, err
	}
	embedder := retrieval.embedder
	proof := RAGProvenance{Version: 1, Snapshot: corpus.Snapshot, Chunks: map[string]ChunkSource{}}
	chunks := []rag.Chunk{}
	for _, record := range corpus.records {
		for _, section := range record.sections {
			if strings.Contains(section.Name, ".") {
				continue
			}
			text := record.YAML[section.Start:section.End]
			for start := 0; start < len(text); {
				end := min(start+4096, len(text))
				for end < len(text) && !utf8.RuneStart(text[end]) {
					end--
				}
				digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", record.Registry, record.Path, section.Name, start)))
				id := hex.EncodeToString(digest[:])
				chunks = append(chunks, rag.Chunk{ID: id, Kind: "record", Registry: record.Registry, Feature: record.ID, Path: record.Path, Facet: section.Name, Text: text[start:end]})
				proof.Chunks[id] = ChunkSource{SHA256: corpus.files[record.Path].SHA256, Category: record.Category, Start: section.Start + start, End: section.Start + end}
				start = end
			}
		}
	}
	if len(chunks) == 0 || len(chunks) > 10000 {
		return nil, proof, errors.New("published index requires 1..10000 chunks")
	}
	vectors := [][]float32{}
	// Bound provider responses and temporary text batches; no automatic provider fallback.
	for start := 0; start < len(chunks); start += 4 {
		texts := []string{}
		for _, chunk := range chunks[start:min(start+4, len(chunks))] {
			texts = append(texts, chunk.Text)
		}
		result, err := embedder.Embed(ctx, texts)
		if err != nil {
			return nil, proof, errors.New("publication embedding failed")
		}
		if len(result) != len(texts) {
			return nil, proof, errors.New("embedding count mismatch")
		}
		for _, vector := range result {
			if len(vector) > 8192 {
				return nil, proof, errors.New("embedding dimension exceeds publication limit")
			}
		}
		vectors = append(vectors, result...)
		if len(vectors) > 0 && int64(len(vectors))*int64(len(vectors[0]))*4 > 64<<20 {
			return nil, proof, errors.New("embedding index exceeds publication budget")
		}
	}
	provider, model := embedder.Describe()
	store, err := rag.NewStore(provider, model, "", chunks, vectors)
	return store, proof, err
}
