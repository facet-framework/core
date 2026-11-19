package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// project writes a throwaway registry: one record, and whatever else a test
// asks for. Small on purpose — the chunker's job is a shape, and a large
// fixture would hide which field produced which chunk.
func project(t *testing.T, files map[string]string) facet.Registry {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return facet.Open(root)
}

const oneRecord = `id: billing/invoice/dunning
schema_version: "0.1"
lifecycle: active
intent:
  summary: |
    Chases an unpaid invoice on a schedule, and stops the moment it is paid.
contract:
  scenarios:
    - id: a-paid-invoice-stops-the-schedule
      statement: |
        Given an invoice with a dunning schedule
        When the invoice is paid
        Then no further reminder is sent
verification:
  unit_tests:
    - id: dunning_test.go::TestPaidStops
      proves: [a-paid-invoice-stops-the-schedule]
observation:
  slos:
    - id: reminder-latency
      target: a reminder goes out within one hour of falling due
knowledge:
  common_pitfalls:
    question: What goes wrong most often?
    answer: A timezone that makes a reminder fire a day early.
lineage:
  owner:
    team: Billing
`

func TestChunksSplitARecordByFacet(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})

	chunks, err := Chunks(reg, false)
	if err != nil {
		t.Fatal(err)
	}

	byFacet := map[string]Chunk{}
	for _, c := range chunks {
		byFacet[c.Facet] = c
	}
	for _, want := range []string{"intent", "contract", "verification", "observation", "knowledge", "lineage"} {
		if _, ok := byFacet[want]; !ok {
			t.Fatalf("no chunk for the %s facet; got %d chunk(s)", want, len(chunks))
		}
	}
	// A facet the record does not declare must not become an empty chunk: an
	// index full of headers with no body ranks noise against real answers.
	if _, ok := byFacet["flags"]; ok {
		t.Fatal("a facet the record does not declare produced a chunk")
	}

	contract := byFacet["contract"]
	if contract.Feature != "billing/invoice/dunning" || contract.Kind != "record" {
		t.Fatalf("wrong provenance: %+v", contract)
	}
	if contract.Path != "docs/facets/features/billing/invoice/dunning.yaml" {
		t.Fatalf("path must be repository-relative, got %q", contract.Path)
	}
	if !strings.Contains(contract.Text, "no further reminder is sent") {
		t.Fatalf("the scenario statement is missing from the contract chunk:\n%s", contract.Text)
	}
}

// The header is what makes a retrieved facet self-contained. Without it an
// `observation` hit is a latency target with no subject.
func TestEveryChunkNamesItsFeatureAndItsFacet(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})
	chunks, err := Chunks(reg, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if !strings.HasPrefix(c.Text, "feature: billing/invoice/dunning\n") {
			t.Fatalf("chunk %s does not open by naming its feature:\n%s", c.ID, c.Text)
		}
		if !strings.Contains(c.Text, "facet: "+c.Facet+"\n") {
			t.Fatalf("chunk %s does not name its facet", c.ID)
		}
		if c.Facet != "intent" && !strings.Contains(c.Text, "about: Chases an unpaid invoice") {
			t.Fatalf("chunk %s carries no summary of the feature it belongs to", c.ID)
		}
	}
}

func TestChunksAreStableAcrossRuns(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})
	first, err := Chunks(reg, false)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Chunks(reg, false)

	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d chunks", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].Text != second[i].Text {
			t.Fatalf("chunk %d differs between runs: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
}

func TestLongFacetsAreSplitWithTheHeaderRepeated(t *testing.T) {
	var scenarios strings.Builder
	scenarios.WriteString("id: big/one/feature\nschema_version: \"0.1\"\nlifecycle: active\nintent:\n  summary: A feature with a very long contract.\ncontract:\n  scenarios:\n")
	for i := 0; i < 40; i++ {
		scenarios.WriteString("    - id: scenario-number-" + itoa(i) + "\n      statement: |\n        Given the system is running\n        When request number " + itoa(i) + " arrives\n        Then it is answered\n")
	}
	reg := project(t, map[string]string{
		"docs/facets/features/big/one/feature.yaml": scenarios.String(),
	})

	chunks, err := Chunks(reg, false)
	if err != nil {
		t.Fatal(err)
	}

	var parts []Chunk
	for _, c := range chunks {
		if c.Facet == "contract" {
			parts = append(parts, c)
		}
	}
	if len(parts) < 2 {
		t.Fatalf("a contract of 40 scenarios should split, got %d part(s)", len(parts))
	}
	for i, p := range parts {
		if p.Part != i {
			t.Fatalf("part numbers must run 0,1,2…; part %d says %d", i, p.Part)
		}
		if !strings.HasPrefix(p.Text, "feature: big/one/feature\n") {
			t.Fatalf("part %d lost its header — a part retrieved alone must still say what it is", i)
		}
		if len(p.Text) > MaxChunkChars*2 {
			t.Fatalf("part %d is %d chars, well past the %d budget", i, len(p.Text), MaxChunkChars)
		}
	}
	if parts[0].ID != "big/one/feature#contract" || parts[1].ID != "big/one/feature#contract/1" {
		t.Fatalf("part ids are not stable: %q, %q", parts[0].ID, parts[1].ID)
	}
}

func TestGeneratedDocumentsAreIndexedFromTheManifest(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
		"docs/help/dunning.md":                              "# Dunning\n\nWhen an invoice falls due we send a reminder, then a second one after a week.\n",
		"docs/facets/generated.lock.yaml": `schema_version: "0.1"
artifacts:
  - subject:
      path: docs/help/dunning.md
      digest:
        sha256: abc
    feature: billing/invoice/dunning
    slots: [common_pitfalls]
    generator:
      pipeline: facet-knowledge
    inputs: []
  - subject:
      path: docs/help/gone.md
      digest:
        sha256: def
    feature: billing/invoice/dunning
    generator:
      pipeline: facet-knowledge
    inputs: []
`,
	})

	chunks, err := Chunks(reg, false)
	if err != nil {
		t.Fatal(err)
	}

	var docs []Chunk
	for _, c := range chunks {
		if c.Kind == "doc" {
			docs = append(docs, c)
		}
	}
	if len(docs) != 1 {
		t.Fatalf("want exactly the one artifact that exists on disk, got %d", len(docs))
	}
	doc := docs[0]
	if doc.Path != "docs/help/dunning.md" || doc.Feature != "billing/invoice/dunning" {
		t.Fatalf("wrong provenance on the document chunk: %+v", doc)
	}
	if !strings.Contains(doc.Text, "generated from feature: billing/invoice/dunning") {
		t.Fatalf("a document chunk must say which feature it came from:\n%s", doc.Text)
	}
	if !strings.Contains(doc.Text, "a second one after a week") {
		t.Fatalf("the article's text is missing:\n%s", doc.Text)
	}
}

func TestBuildAndQueryFindTheRightFacet(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})
	fake := &fakeEmbedder{dim: 64}

	store, err := Build(context.Background(), []facet.Registry{reg}, fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.Provider != "fake" || store.Model != "fixed" {
		t.Fatalf("the index must record what built it, got %s/%s", store.Provider, store.Model)
	}
	if store.Len() == 0 {
		t.Fatal("nothing was indexed")
	}

	hits, err := Query(context.Background(), store, fake, "reminder latency target", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		if h.Feature != "billing/invoice/dunning" {
			t.Fatalf("a hit named a feature that is not in the project: %q", h.Feature)
		}
	}
}

// Two models produce two coordinate systems. Comparing across them returns
// ranked, plausible nonsense, so it must be refused rather than answered.
func TestQueryRefusesAModelTheIndexWasNotBuiltWith(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})
	store, err := Build(context.Background(), []facet.Registry{reg}, &fakeEmbedder{dim: 64}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Query(context.Background(), store, &LocalEmbedder{}, "anything", 3)
	if err == nil {
		t.Fatal("querying with another model must be refused")
	}
	if !strings.Contains(err.Error(), "facet rag index") {
		t.Fatalf("the error must say how to fix it, got %v", err)
	}
}

func TestBuildReportsProgress(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/billing/invoice/dunning.yaml": oneRecord,
	})
	var phases []string
	_, err := Build(context.Background(), []facet.Registry{reg}, &fakeEmbedder{dim: 8},
		func(phase string, done, total int) { phases = append(phases, phase) })
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) < 2 || phases[0] != "read" {
		t.Fatalf("want a read phase then an embed phase, got %v", phases)
	}
}

func TestBuildSaysSoWhenThereIsNothingToIndex(t *testing.T) {
	reg := project(t, map[string]string{"README.md": "no records here"})
	_, err := Build(context.Background(), []facet.Registry{reg}, &fakeEmbedder{dim: 8}, nil)
	if err == nil {
		t.Fatal("an empty project must be an error with a hint, not an empty index")
	}
	if !strings.Contains(err.Error(), "facet init") {
		t.Fatalf("the error must point somewhere useful, got %v", err)
	}
}
