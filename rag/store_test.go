package rag

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// vec builds a vector of the given width with the listed leading values.
func vec(dim int, values ...float32) []float32 {
	v := make([]float32, dim)
	copy(v, values)
	return v
}

func chunksNamed(names ...string) []Chunk {
	out := make([]Chunk, len(names))
	for i, n := range names {
		out[i] = Chunk{ID: n, Feature: n, Kind: "record", Facet: "intent", Path: n + ".yaml", Text: n}
	}
	return out
}

func TestSearchRanksByCosineSimilarity(t *testing.T) {
	// Three fixed, hand-written vectors: one pointing along x, one along y, one
	// between them. Nothing here depends on an embedding model, which is the
	// point — the ranking is arithmetic and must be testable as arithmetic.
	store, err := NewStore("test", "fixed", "2026-01-01T00:00:00Z",
		chunksNamed("east", "north", "north-east"),
		[][]float32{vec(4, 1, 0), vec(4, 0, 1), vec(4, 1, 1)})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search(vec(4, 1, 0), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	if hits[0].ID != "east" || hits[1].ID != "north-east" || hits[2].ID != "north" {
		t.Fatalf("wrong order: %s, %s, %s", hits[0].ID, hits[1].ID, hits[2].ID)
	}
	if math.Abs(float64(hits[0].Score)-1) > 1e-6 {
		t.Fatalf("an identical direction should score 1, got %f", hits[0].Score)
	}
	if math.Abs(float64(hits[2].Score)) > 1e-6 {
		t.Fatalf("an orthogonal direction should score 0, got %f", hits[2].Score)
	}
}

func TestSearchHonoursTheLimit(t *testing.T) {
	store, err := NewStore("test", "fixed", "", chunksNamed("a", "b", "c"),
		[][]float32{vec(2, 1, 0), vec(2, 0.9, 0.1), vec(2, 0.8, 0.2)})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search(vec(2, 1, 0), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].ID != "a" || hits[1].ID != "b" {
		t.Fatalf("want the two nearest, got %s and %s", hits[0].ID, hits[1].ID)
	}
}

// The parallel path and the single-threaded one must not disagree. They are two
// implementations of the same ranking, and the shard boundary is exactly where
// a top-k merge goes wrong.
func TestSearchAgreesAcrossTheParallelThreshold(t *testing.T) {
	// Derive the size from shardThreshold so this always exercises sharding.
	const n = shardThreshold * 3
	if n < shardThreshold {
		t.Fatalf("this case must exercise the sharded path: n=%d is below shardThreshold=%d", n, shardThreshold)
	}
	chunks := make([]Chunk, n)
	vectors := make([][]float32, n)
	for i := range chunks {
		chunks[i] = Chunk{ID: string(rune('a'+i%26)) + itoa(i)}
		// A deterministic spread so a known document is the nearest neighbour.
		vectors[i] = vec(8, float32(i%97)/97, float32(i%31)/31, float32(i%7)/7)
	}
	// Planted past the first shard boundary, so the answer has to survive the
	// merge rather than being found by whichever worker got the first slice.
	planted := shardThreshold + shardThreshold/2
	vectors[planted] = vec(8, 0, 0, 0, 1)

	store, err := NewStore("test", "fixed", "", chunks, vectors)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search(vec(8, 0, 0, 0, 1), 1)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].ID != chunks[planted].ID {
		t.Fatalf("the sharded scan missed the planted neighbour: got %s", hits[0].ID)
	}
	if math.Abs(float64(hits[0].Score)-1) > 1e-5 {
		t.Fatalf("planted neighbour should score 1, got %f", hits[0].Score)
	}
}

func TestSearchRefusesAQueryOfTheWrongWidth(t *testing.T) {
	store, err := NewStore("test", "fixed", "", chunksNamed("a"), [][]float32{vec(4, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search(vec(8, 1), 1); err == nil {
		t.Fatal("a query of the wrong width must be refused, not answered")
	}
}

func TestNewStoreRefusesRaggedVectors(t *testing.T) {
	_, err := NewStore("test", "fixed", "", chunksNamed("a", "b"),
		[][]float32{vec(4, 1), vec(8, 1)})
	if err == nil {
		t.Fatal("vectors of two widths must be refused: they cannot have come from one model")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "index.bin")

	original, err := NewStore("openai-compatible", "text-embedding-3-small", "2026-02-03T04:05:06Z",
		[]Chunk{
			{ID: "one#intent", Feature: "one", Kind: "record", Facet: "intent", Path: "a.yaml", Title: "one", Text: "the first"},
			{ID: "doc:b.md", Kind: "doc", Facet: "article", Path: "b.md", Part: 1, Title: "b.md", Text: "the second"},
		},
		[][]float32{vec(3, 1, 0, 0), vec(3, 0, 1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != original.Model || loaded.Provider != original.Provider {
		t.Fatalf("the index forgot what built it: %s/%s", loaded.Provider, loaded.Model)
	}
	if loaded.Dim != 3 || loaded.Len() != 2 {
		t.Fatalf("want 2 chunks at 3 dimensions, got %d at %d", loaded.Len(), loaded.Dim)
	}
	if loaded.BuiltAt != "2026-02-03T04:05:06Z" {
		t.Fatalf("built-at not round-tripped: %q", loaded.BuiltAt)
	}

	hits, err := loaded.Search(vec(3, 0, 1, 0), 1)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].ID != "doc:b.md" || hits[0].Kind != "doc" || hits[0].Part != 1 {
		t.Fatalf("chunk metadata did not survive the round trip: %+v", hits[0].Chunk)
	}
	if hits[0].Text != "the second" {
		t.Fatalf("chunk text did not survive: %q", hits[0].Text)
	}
}

func TestLoadRejectsSomethingThatIsNotAnIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-index.bin")
	if err := os.WriteFile(path, []byte("records:\n  - a.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStore(path); err == nil {
		t.Fatal("a YAML file read as an index must be refused")
	}
}

func TestLoadRejectsANewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.bin")
	body := []byte(magic)
	body = binary.LittleEndian.AppendUint32(body, formatVersion+1)
	body = binary.LittleEndian.AppendUint32(body, 2)
	body = binary.LittleEndian.AppendUint32(body, 2)
	body = append(body, "{}[]"...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStore(path)
	if err == nil {
		t.Fatal("an index from a newer facet must be refused rather than misread")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
