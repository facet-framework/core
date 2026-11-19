package remote

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/facet-framework/core/rag"
)

type RAGConfig struct {
	Index            string `json:"index"`
	IndexSHA256      string `json:"index_sha256"`
	Provenance       string `json:"provenance"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	BaseURL          string `json:"base_url,omitempty"`
	AllowQueryEgress bool   `json:"allow_query_egress"`
	APIKey           string `json:"-"`
}
type RAGProvenance struct {
	Version  int                    `json:"version"`
	Snapshot string                 `json:"snapshot"`
	Chunks   map[string]ChunkSource `json:"chunks"`
}
type ChunkSource struct {
	SHA256   string `json:"sha256"`
	Category string `json:"category"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}
type SearchHit struct {
	ID          string  `json:"id"`
	QualifiedID string  `json:"qualified_id"`
	Registry    string  `json:"registry"`
	Category    string  `json:"category"`
	Path        string  `json:"path"`
	Section     string  `json:"section"`
	Text        string  `json:"text"`
	Score       float32 `json:"score"`
}
type SearchResult struct {
	Snapshot string      `json:"snapshot"`
	Model    string      `json:"model"`
	Hits     []SearchHit `json:"hits"`
}
type Retrieval struct {
	state    string
	store    *rag.Store
	embedder rag.Embedder
	egress   bool
	hits     map[string]SearchHit
}

func loadRetrieval(c *Corpus, cfg *RAGConfig) (*Retrieval, error) {
	r := &Retrieval{state: "disabled"}
	if cfg == nil {
		return r, nil
	}
	if err := r.configure(cfg); err != nil {
		return nil, err
	}
	data, err := boundedFile(cfg.Index, 64<<20)
	if os.IsNotExist(err) {
		r.state = "missing_index"
		return r, nil
	}
	if err != nil {
		r.state = "stale_index"
		return r, nil
	}
	r.state = "stale_index"
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != cfg.IndexSHA256 {
		return r, nil
	}
	store, err := decodeStore(data)
	if err != nil {
		return r, nil
	}
	sidecar, err := boundedFile(cfg.Provenance, 4<<20)
	if err != nil {
		return r, nil
	}
	var provenance RAGProvenance
	if json.Unmarshal(sidecar, &provenance) != nil || provenance.Version != 1 || provenance.Snapshot != c.Snapshot || len(provenance.Chunks) != store.Len() {
		return r, nil
	}
	provider, model := r.embedder.Describe()
	if store.Provider != provider || store.Model != model {
		return r, nil
	}
	r.hits = map[string]SearchHit{}
	for _, chunk := range store.Chunks() {
		proof, ok := provenance.Chunks[chunk.ID]
		if !ok || chunk.Kind != "record" {
			return r, nil
		}
		entry, ok := c.files[chunk.Path]
		if !ok || entry.Kind == "source" || entry.Kind != proof.Category || entry.SHA256 != proof.SHA256 || entry.Registry != chunk.Registry {
			return r, nil
		}
		record, err := c.record(chunk.Registry+":"+chunk.Feature, proof.Category)
		if err != nil || record.Path != chunk.Path {
			return r, nil
		}
		section, ok := record.section(chunk.Facet)
		if !ok || proof.Start < section.Start || proof.End <= proof.Start || proof.End > section.End {
			return r, nil
		}
		text := record.YAML[proof.Start:proof.End]
		if !utf8.ValidString(text) || text != chunk.Text {
			return r, nil
		}
		if _, duplicate := r.hits[chunk.ID]; duplicate {
			return r, nil
		}
		r.hits[chunk.ID] = SearchHit{ID: record.ID, QualifiedID: qualified(record), Registry: record.Registry, Category: record.Category, Path: record.Path, Section: chunk.Facet, Text: text}
	}
	r.store = store
	r.state = "ready"
	return r, nil
}
func (r *Retrieval) search(ctx context.Context, snapshot string, in Input) (SearchResult, error) {
	if utf8.RuneCountInString(in.Query) > 4096 || strings.TrimSpace(in.Query) == "" || in.Limit < 0 || in.Limit > 20 {
		return SearchResult{}, problem(422, "invalid_selection")
	}
	switch r.state {
	case "disabled":
		return SearchResult{}, problem(503, "rag_disabled")
	case "missing_index":
		return SearchResult{}, problem(503, "index_missing")
	case "stale_index":
		return SearchResult{}, problem(409, "index_stale")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 5
	}
	hits, err := rag.Query(ctx, r.store, r.embedder, in.Query, limit)
	if err != nil {
		return SearchResult{}, problem(503, "provider_unavailable")
	}
	out := SearchResult{Snapshot: snapshot, Model: r.store.Provider + "/" + r.store.Model, Hits: []SearchHit{}}
	for _, hit := range hits {
		copy := r.hits[hit.ID]
		copy.Score = max(-1, min(1, hit.Score))
		out.Hits = append(out.Hits, copy)
	}
	return out, nil
}

// decodeStore validates all allocation lengths before delegating vector validation.
// It reads the existing FACETVEC v1 format without exposing its path-based reader.
func decodeStore(data []byte) (*rag.Store, error) {
	invalid := errors.New("invalid bounded RAG index")
	if len(data) < 20 || string(data[:8]) != "FACETVEC" || binary.LittleEndian.Uint32(data[8:12]) != 1 {
		return nil, invalid
	}
	headSize, metaSize := int64(binary.LittleEndian.Uint32(data[12:16])), int64(binary.LittleEndian.Uint32(data[16:20]))
	if headSize > 65536 || metaSize > 32<<20 || 20+headSize+metaSize > int64(len(data)) {
		return nil, invalid
	}
	var head struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BuiltAt  string `json:"builtAt"`
		Dim      int    `json:"dim"`
		Count    int    `json:"count"`
	}
	if json.Unmarshal(data[20:20+headSize], &head) != nil || head.Dim < 1 || head.Dim > 65536 || head.Count < 1 || head.Count > 100000 {
		return nil, invalid
	}
	vectorBytes := int64(head.Dim) * int64(head.Count) * 4
	offset := 20 + headSize + metaSize
	if offset+vectorBytes != int64(len(data)) {
		return nil, invalid
	}
	var chunks []rag.Chunk
	if json.Unmarshal(data[20+headSize:offset], &chunks) != nil || len(chunks) != head.Count {
		return nil, invalid
	}
	vectors := make([][]float32, head.Count)
	for i := range vectors {
		vectors[i] = make([]float32, head.Dim)
		for j := range vectors[i] {
			value := math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, invalid
			}
			vectors[i][j] = value
			offset += 4
		}
	}
	return rag.NewStore(head.Provider, head.Model, head.BuiltAt, chunks, vectors)
}
func boundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > limit {
		return nil, errors.New("invalid or oversized file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("oversized file")
	}
	return data, err
}

func (r *Retrieval) configure(cfg *RAGConfig) error {
	switch cfg.Provider {
	case "local":
		if cfg.Model != "" && cfg.Model != "facet-lexical-v1" {
			return errors.New("local RAG model must be facet-lexical-v1")
		}
		r.embedder = &rag.LocalEmbedder{}
	case "http":
		if !cfg.AllowQueryEgress || !httpsURL(cfg.BaseURL) || cfg.Model == "" {
			return errors.New("remote embedding requires explicit HTTPS egress opt-in and model")
		}
		r.egress = true
		r.embedder = &rag.HTTPEmbedder{BaseURL: cfg.BaseURL, Model: cfg.Model, APIKey: cfg.APIKey, Client: &http.Client{Transport: boundedTransport{trustedTransport()}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}}
	default:
		return errors.New("unsupported RAG provider")
	}
	return nil
}
