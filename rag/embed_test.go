package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEmbedder is what every test that is not about HTTP uses.
type fakeEmbedder struct {
	dim   int
	calls [][]string
	fail  error
}

func (f *fakeEmbedder) Describe() (string, string) { return "fake", "fixed" }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.calls = append(f.calls, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, text := range texts {
		v := make([]float32, f.dim)
		for _, token := range tokenise(text) {
			v[int(token[0])%f.dim] += 1
		}
		out[i] = v
	}
	return out, nil
}

func TestHTTPEmbedderSpeaksTheOpenAIDialect(t *testing.T) {
	var got embedRequest
	var auth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("want /v1/embeddings, got %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		// Returned out of order on purpose: the response carries an index per
		// item and a client that appends rather than places would give every
		// chunk somebody else's vector.
		_ = json.NewEncoder(w).Encode(embedResponse{Data: []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}{
			{Index: 1, Embedding: []float32{0, 1}},
			{Index: 0, Embedding: []float32{1, 0}},
		}})
	}))
	defer server.Close()

	e := &HTTPEmbedder{BaseURL: server.URL + "/v1", Model: "text-embedding-3-small", APIKey: "secret", Dimensions: 2}
	vectors, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != "text-embedding-3-small" || len(got.Input) != 2 || got.Dimensions != 2 {
		t.Fatalf("wrong request: %+v", got)
	}
	if auth != "Bearer secret" {
		t.Fatalf("want a bearer token, got %q", auth)
	}
	if vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("vectors were not placed by index: %v", vectors)
	}
}

func TestHTTPEmbedderBatches(t *testing.T) {
	var batches []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		batches = append(batches, len(req.Input))

		data := make([]struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))
		for i := range data {
			data[i].Index = i
			data[i].Embedding = []float32{1}
		}
		_ = json.NewEncoder(w).Encode(embedResponse{Data: data})
	}))
	defer server.Close()

	e := &HTTPEmbedder{BaseURL: server.URL, Model: "m", Batch: 2}
	texts := []string{"a", "b", "c", "d", "e"}
	vectors, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 5 {
		t.Fatalf("want 5 vectors, got %d", len(vectors))
	}
	if len(batches) != 3 || batches[0] != 2 || batches[2] != 1 {
		t.Fatalf("want batches of 2,2,1, got %v", batches)
	}
}

// A provider's own message is the one sentence that says what went wrong. A
// client that swallows it and reports "request failed" makes a typo in a model
// name an afternoon's work.
func TestHTTPEmbedderReportsTheProvidersOwnMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid model name passed in model=nope"}}`))
	}))
	defer server.Close()

	e := &HTTPEmbedder{BaseURL: server.URL, Model: "nope"}
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	if !strings.Contains(err.Error(), "Invalid model name passed in model=nope") {
		t.Fatalf("the provider's message was swallowed: %v", err)
	}
}

func TestHTTPEmbedderRefusesAShortResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer server.Close()

	e := &HTTPEmbedder{BaseURL: server.URL, Model: "m"}
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("two inputs and one vector must be an error, not a silent misalignment")
	}
}

func TestHTTPEmbedderNeedsAnEndpointAndAModel(t *testing.T) {
	if _, err := (&HTTPEmbedder{Model: "m"}).Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("no base URL must be refused")
	}
	if _, err := (&HTTPEmbedder{BaseURL: "http://x"}).Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("no model must be refused")
	}
}

func TestLocalEmbedderIsDeterministicAndNormalisable(t *testing.T) {
	e := &LocalEmbedder{}
	first, err := e.Embed(context.Background(), []string{"the retry policy for failed delivery"})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := e.Embed(context.Background(), []string{"the retry policy for failed delivery"})

	if len(first[0]) != LocalDim {
		t.Fatalf("want %d dimensions, got %d", LocalDim, len(first[0]))
	}
	for i := range first[0] {
		if first[0][i] != second[0][i] {
			t.Fatal("the same text must embed to the same vector, or an index cannot be rebuilt")
		}
	}
}

// The local embedder is lexical, and this is what lexical buys: shared words
// score higher than unshared ones. Asserted so a change to the hashing cannot
// quietly turn it into noise.
func TestLocalEmbedderPutsSharedWordsCloser(t *testing.T) {
	e := &LocalEmbedder{}
	vectors, err := e.Embed(context.Background(), []string{
		"the retry policy for a failed delivery attempt",
		"retry policy on delivery failure",
		"the colour of the button on the settings page",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore("local", "facet-lexical-v1", "",
		chunksNamed("retry-a", "retry-b", "button"), vectors)
	if err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search(vectors[0], 3)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].ID != "retry-a" {
		t.Fatalf("a text must be its own nearest neighbour, got %s", hits[0].ID)
	}
	if hits[1].ID != "retry-b" {
		t.Fatalf("want the text sharing words second, got %s", hits[1].ID)
	}
	if hits[1].Score <= hits[2].Score {
		t.Fatalf("shared words scored no better than none: %f vs %f", hits[1].Score, hits[2].Score)
	}
}

func TestTokeniseDropsPunctuationAndSingleCharacters(t *testing.T) {
	got := tokenise("Given a 'retry', When it FAILS — x.")
	want := []string{"given", "retry", "when", "it", "fails"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}
