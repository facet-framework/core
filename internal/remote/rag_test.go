package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRAGSnapshotProvenanceAndRecordOnlyResults(t *testing.T) {
	root, pub := fixture(t)
	corpus, err := LoadCorpus(root, pub)
	if err != nil {
		t.Fatal(err)
	}
	store, provenance, err := BuildIndex(context.Background(), corpus, &RAGConfig{Provider: "local"}, false)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	index := filepath.Join(dir, "index.bin")
	if err := store.Save(index); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)

	sidecar := filepath.Join(dir, "provenance.json")
	write := func() {
		raw, _ := json.Marshal(provenance)
		if err := os.WriteFile(sidecar, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	cfg := &RAGConfig{Index: index, IndexSHA256: hex.EncodeToString(hash[:]), Provenance: sidecar, Provider: "local"}
	retrieval, err := loadRetrieval(corpus, cfg)
	if err != nil || retrieval.state != "ready" {
		t.Fatalf("RAG not ready: %+v %v", retrieval, err)
	}
	result, err := retrieval.search(context.Background(), corpus.Snapshot, Input{Query: "Example", Limit: 1})
	if err != nil || len(result.Hits) != 1 || !strings.Contains(corpus.records[0].YAML, result.Hits[0].Text) || result.Model != "local/facet-lexical-v1" {
		t.Fatalf("bad retrieval: %+v %v", result, err)
	}
	provenance.Snapshot = "different"
	write()
	retrieval, err = loadRetrieval(corpus, cfg)
	if err != nil || retrieval.state != "stale_index" {
		t.Fatal("stale index accepted", err)
	}
	cfg.Index = filepath.Join(dir, "missing")
	retrieval, err = loadRetrieval(corpus, cfg)
	if err != nil || retrieval.state != "missing_index" {
		t.Fatal("missing index state", err)
	}
	if _, err := loadRetrieval(corpus, &RAGConfig{Provider: "http", BaseURL: "https://provider.example", Model: "test"}); err == nil {
		t.Fatal("egress enabled without opt-in")
	}
}
func TestBoundedIndexRejectsAllocationAttacks(t *testing.T) {
	for _, data := range [][]byte{[]byte("invalid"), append([]byte("FACETVEC\x01\x00\x00\x00"), []byte{255, 255, 255, 255, 255, 255, 255, 255}...)} {
		if _, err := decodeStore(data); err == nil {
			t.Fatal("malformed index accepted")
		}
	}
}
