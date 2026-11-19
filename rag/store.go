// Package rag turns a FACET registry into something a question can be asked of
// by meaning rather than by exact id.
//
// @facet: navigation/retrieval/semantic-search
// @facet-role: primary
package rag

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

// magic marks a store file. A wrong file read as an index would otherwise be
// megabytes of nonsense decoded as vectors.
const magic = "FACETVEC"

// shardThreshold is the store size at which Search starts splitting the scan
// across cores. Below it the goroutines cost more than they save.
const shardThreshold = 4096

// formatVersion is the on-disk layout. An index written by a newer facet is
// refused rather than misread.
const formatVersion uint32 = 1

// Chunk is one embeddable unit of text and where it came from.
type Chunk struct {
	// ID is stable across rebuilds: same registry, same feature, same facet,
	// same part number yields the same id. That is what lets an index be
	// rebuilt incrementally later without renumbering everything.
	ID string `json:"id"`
	// Registry is the declaring registry's name, empty in a single-registry
	// project.
	Registry string `json:"registry,omitempty"`
	// Feature is the feature id this text belongs to, empty for a generated
	// document that names no feature.
	Feature string `json:"feature,omitempty"`
	// Kind is "record" for text taken from a FACET record, "doc" for text taken
	// from a generated article. A reader is entitled to know which it got.
	Kind string `json:"kind"`
	// Facet is the record facet the text came from — intent, contract,
	// verification, observation, knowledge, lineage — or "article" for a doc.
	Facet string `json:"facet"`
	// Path is repository-relative, so a hit names a file that is really there.
	Path string `json:"path"`
	// Part is the slice number when one facet was too long for a single chunk.
	Part int `json:"part,omitempty"`
	// Title is the one-line headline shown above a hit.
	Title string `json:"title,omitempty"`
	// Text is what was embedded, verbatim. Stored so a hit can be quoted rather
	// than summarised — the same discipline the rest of this toolchain applies
	// to generated prose.
	Text string `json:"text"`
}

// Hit is one search result.
type Hit struct {
	Chunk
	// Score is cosine similarity in [-1, 1]. Vectors are stored normalised, so
	// this is a plain dot product and needs no division at query time.
	Score float32 `json:"score"`
}

// Store is an index held entirely in memory and searched by brute force.
type Store struct {
	// Provider and Model identify what produced the vectors. A query embedded
	// by a different model than the index is meaningless, and this is how that
	// is caught rather than silently returning nonsense.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
	BuiltAt  string `json:"builtAt"`

	chunks  []Chunk
	vectors []float32
}

// Len reports how many chunks the store holds.
func (s *Store) Len() int { return len(s.chunks) }

// Chunks exposes the stored chunks, for callers that report on the index
// rather than search it.
func (s *Store) Chunks() []Chunk { return s.chunks }

// NewStore builds a store from chunks and their vectors, normalising as it
// goes so that search never has to.
func NewStore(provider, model, builtAt string, chunks []Chunk, vectors [][]float32) (*Store, error) {
	if len(chunks) != len(vectors) {
		return nil, fmt.Errorf("rag: %d chunk(s) but %d vector(s)", len(chunks), len(vectors))
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("rag: nothing to index")
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil, fmt.Errorf("rag: the embedding provider returned an empty vector")
	}

	flat := make([]float32, 0, len(vectors)*dim)
	for i, v := range vectors {
		if len(v) != dim {
			return nil, fmt.Errorf("rag: chunk %q has %d dimension(s), the first has %d — "+
				"the index was built by more than one model", chunks[i].ID, len(v), dim)
		}
		flat = append(flat, v...)
	}
	normaliseRows(flat, dim)

	return &Store{
		Provider: provider,
		Model:    model,
		Dim:      dim,
		BuiltAt:  builtAt,
		chunks:   append([]Chunk(nil), chunks...),
		vectors:  flat,
	}, nil
}

// Search returns the k nearest chunks to a query vector, best first.
func (s *Store) Search(query []float32, k int) ([]Hit, error) {
	if len(query) != s.Dim {
		return nil, fmt.Errorf("rag: the query has %d dimension(s) and the index has %d — "+
			"the index was built with %q; rebuild it or query with the same model",
			len(query), s.Dim, s.Model)
	}
	if k <= 0 {
		k = 5
	}
	if k > len(s.chunks) {
		k = len(s.chunks)
	}

	q := make([]float32, len(query))
	copy(q, query)
	normaliseRows(q, s.Dim)

	// One goroutine per core, but only once there is enough work that starting
	// them is cheaper than the scan. Below that the single-threaded path wins,
	// and a registry with two hundred chunks is very much below it.
	workers := runtime.GOMAXPROCS(0)
	if len(s.chunks) < shardThreshold || workers < 2 {
		return s.scan(q, 0, len(s.chunks), k), nil
	}

	per := (len(s.chunks) + workers - 1) / workers
	parts := make([][]Hit, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w * per
		if lo >= len(s.chunks) {
			break
		}
		hi := lo + per
		if hi > len(s.chunks) {
			hi = len(s.chunks)
		}
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			parts[w] = s.scan(q, lo, hi, k)
		}(w, lo, hi)
	}
	wg.Wait()

	var merged []Hit
	for _, p := range parts {
		merged = append(merged, p...)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	if len(merged) > k {
		merged = merged[:k]
	}
	return merged, nil
}

// scan walks one contiguous span and keeps the best k.
func (s *Store) scan(q []float32, lo, hi, k int) []Hit {
	type cand struct {
		i     int
		score float32
	}
	best := make([]cand, 0, k)
	worst := float32(math.Inf(-1))

	dim := s.Dim
	for i := lo; i < hi; i++ {
		row := s.vectors[i*dim : i*dim+dim : i*dim+dim]
		score := dot(q, row)
		if len(best) == k && score <= worst {
			continue
		}
		pos := sort.Search(len(best), func(j int) bool { return best[j].score < score })
		if len(best) < k {
			best = append(best, cand{})
		}
		copy(best[pos+1:], best[pos:])
		best[pos] = cand{i: i, score: score}
		worst = best[len(best)-1].score
	}

	hits := make([]Hit, len(best))
	for i, c := range best {
		hits[i] = Hit{Chunk: s.chunks[c.i], Score: c.score}
	}
	return hits
}

// dot is the whole hot loop.
func dot(a, b []float32) float32 {
	b = b[:len(a)]
	var sum float32
	for i, v := range a {
		sum += v * b[i]
	}
	return sum
}

// normaliseRows scales every row of a flat matrix to unit length, in place.
// A zero row is left alone: it scores zero against everything, which is the
// right answer for a chunk whose text carried no signal at all.
func normaliseRows(flat []float32, dim int) {
	for off := 0; off+dim <= len(flat); off += dim {
		row := flat[off : off+dim]
		var sum float64
		for _, v := range row {
			sum += float64(v) * float64(v)
		}
		if sum == 0 {
			continue
		}
		inv := float32(1 / math.Sqrt(sum))
		for i := range row {
			row[i] *= inv
		}
	}
}

// header is the store's self-description, written as JSON so a curious reader
// can find out what an index holds with `head -c 200`.
type header struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
	Count    int    `json:"count"`
	BuiltAt  string `json:"builtAt"`
}

// Save writes the store to one file.
func (s *Store) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".facet-rag-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if err := s.write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *Store) write(w io.Writer) error {
	head, err := json.Marshal(header{
		Provider: s.Provider, Model: s.Model,
		Dim: s.Dim, Count: len(s.chunks), BuiltAt: s.BuiltAt,
	})
	if err != nil {
		return err
	}
	meta, err := json.Marshal(s.chunks)
	if err != nil {
		return err
	}

	if _, err := io.WriteString(w, magic); err != nil {
		return err
	}
	for _, n := range []uint32{formatVersion, uint32(len(head)), uint32(len(meta))} {
		if err := binary.Write(w, binary.LittleEndian, n); err != nil {
			return err
		}
	}
	if _, err := w.Write(head); err != nil {
		return err
	}
	if _, err := w.Write(meta); err != nil {
		return err
	}

	// Vectors are written as raw little-endian float32 rather than through the
	// JSON above, because a hundred thousand of them is three hundred megabytes
	// and JSON would make it a gigabyte of decimal digits.
	buf := make([]byte, 4*s.Dim)
	for off := 0; off < len(s.vectors); off += s.Dim {
		row := s.vectors[off : off+s.Dim]
		for i, v := range row {
			binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
		}
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// LoadStore reads an index from disk.
func LoadStore(path string) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readStore(f, path)
}

func readStore(r io.Reader, path string) (*Store, error) {
	mark := make([]byte, len(magic))
	if _, err := io.ReadFull(r, mark); err != nil {
		return nil, fmt.Errorf("%s: not a FACET index: %w", path, err)
	}
	if string(mark) != magic {
		return nil, fmt.Errorf("%s: not a FACET index", path)
	}

	var version, headLen, metaLen uint32
	for _, n := range []*uint32{&version, &headLen, &metaLen} {
		if err := binary.Read(r, binary.LittleEndian, n); err != nil {
			return nil, fmt.Errorf("%s: truncated index: %w", path, err)
		}
	}
	if version != formatVersion {
		return nil, fmt.Errorf("%s: index format v%d, this facet reads v%d — rebuild it with 'facet rag index'",
			path, version, formatVersion)
	}

	head := make([]byte, headLen)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("%s: truncated index: %w", path, err)
	}
	var h header
	if err := json.Unmarshal(head, &h); err != nil {
		return nil, fmt.Errorf("%s: unreadable index header: %w", path, err)
	}

	meta := make([]byte, metaLen)
	if _, err := io.ReadFull(r, meta); err != nil {
		return nil, fmt.Errorf("%s: truncated index: %w", path, err)
	}
	var chunks []Chunk
	if err := json.Unmarshal(meta, &chunks); err != nil {
		return nil, fmt.Errorf("%s: unreadable index metadata: %w", path, err)
	}
	if len(chunks) != h.Count {
		return nil, fmt.Errorf("%s: header says %d chunk(s), metadata holds %d", path, h.Count, len(chunks))
	}

	raw := make([]byte, 4*h.Dim*h.Count)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, fmt.Errorf("%s: truncated vectors: %w", path, err)
	}
	vectors := make([]float32, h.Dim*h.Count)
	for i := range vectors {
		vectors[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}

	return &Store{
		Provider: h.Provider, Model: h.Model, Dim: h.Dim, BuiltAt: h.BuiltAt,
		chunks: chunks, vectors: vectors,
	}, nil
}
