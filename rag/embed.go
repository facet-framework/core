package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Embedder turns text into vectors.
//
// @facet: navigation/retrieval/embedding-configuration
// @facet-role: primary
type Embedder interface {
	// Embed returns one vector per input, in order. Implementations batch as
	// they see fit; the contract is only that the order is preserved.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Describe names what produced the vectors, as "provider" and "model".
	// The pair is written into the index so a query embedded by something else
	// is refused rather than answered wrongly.
	Describe() (provider, model string)
}

// ---------------------------------------------------------------------------
// The HTTP embedder
// ---------------------------------------------------------------------------

// HTTPEmbedder calls an OpenAI-compatible /embeddings endpoint.
type HTTPEmbedder struct {
	BaseURL string
	Model   string
	// APIKey is the bearer token. Empty is valid and normal: a local Ollama
	// wants no key at all.
	APIKey string
	// Dimensions is passed through when non-zero, for the providers that let a
	// caller shorten the vector. Providers that do not understand it ignore it.
	Dimensions int
	// Batch is how many texts go in one request. Zero means the default.
	Batch  int
	Client *http.Client
}

// DefaultBatch is how many chunks one request carries.
const DefaultBatch = 64

// DefaultTimeout bounds one embedding request.
const DefaultTimeout = 60 * time.Second

type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Describe names the provider kind and the model, so an index says what
// produced its vectors without carrying a key or a URL.
func (e *HTTPEmbedder) Describe() (string, string) { return "openai-compatible", e.Model }

// Embed sends the texts in batches and returns the vectors in input order.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.Model == "" {
		return nil, fmt.Errorf("rag: no embedding model configured — set embedding.model in %s or pass --embed-model", ConfigHint)
	}
	if strings.TrimSpace(e.BaseURL) == "" {
		return nil, fmt.Errorf("rag: no embedding endpoint configured — set embedding.base_url in %s or pass --embed-url", ConfigHint)
	}

	batch := e.Batch
	if batch <= 0 {
		batch = DefaultBatch
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.one(ctx, client, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *HTTPEmbedder) one(ctx context.Context, client *http.Client, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.Model, Input: texts, Dimensions: e.Dimensions})
	if err != nil {
		return nil, err
	}

	url := strings.TrimSuffix(e.BaseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rag: %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Bounded, because a misconfigured base URL pointing at something that is
	// not an embeddings endpoint can stream a very large HTML page.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}

	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Error != nil || resp.StatusCode != http.StatusOK {
		// The provider's own message is reported verbatim. A wrapped "request
		// failed" would hide the one sentence that says which model name was
		// wrong, and that sentence is the entire value of the error.
		message := strings.TrimSpace(string(raw))
		if parsed.Error != nil && parsed.Error.Message != "" {
			message = parsed.Error.Message
		}
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		return nil, fmt.Errorf("rag: %s returned %s: %s", url, resp.Status, message)
	}

	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("rag: asked %s for %d vector(s) and got %d", url, len(texts), len(parsed.Data))
	}

	// The response carries an index per item, and providers are not obliged to
	// return them in order. Placing by index rather than appending costs
	// nothing and removes a class of silent mis-association where every chunk
	// gets somebody else's vector.
	out := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, fmt.Errorf("rag: %s returned an out-of-range index %d", url, item.Index)
		}
		out[item.Index] = item.Embedding
	}
	for i, v := range out {
		if len(v) == 0 {
			return nil, fmt.Errorf("rag: %s returned no vector for input %d", url, i)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The local embedder
// ---------------------------------------------------------------------------

// LocalDim is the width of the built-in embedder's vectors.
const LocalDim = 512

// LocalEmbedder is a lexical embedder that runs in this process.
//
// @facet: navigation/retrieval/embedding-configuration
// @facet-role: related
type LocalEmbedder struct {
	// Dim is the vector width. Zero uses LocalDim.
	Dim int
}

// Describe names this embedder in the index, so a store built with it is
// visibly not a store built with a neural model.
func (l *LocalEmbedder) Describe() (string, string) { return "local", "facet-lexical-v1" }

// Embed hashes each text into a vector. It never fails and never blocks.
func (l *LocalEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	dim := l.Dim
	if dim <= 0 {
		dim = LocalDim
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = l.vector(text, dim)
	}
	return out, nil
}

func (l *LocalEmbedder) vector(text string, dim int) []float32 {
	counts := map[uint32]float64{}
	tokens := tokenise(text)

	add := func(term string) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(term))
		sum := h.Sum32()
		// The low bits pick the bucket and the top bit picks the sign. A signed
		// projection is what keeps unrelated terms that collide into the same
		// bucket from reliably reinforcing each other.
		counts[sum]++
	}
	for i, t := range tokens {
		add(t)
		if i+1 < len(tokens) {
			// Word pairs carry the little order information a bag of words
			// throws away: "unbacked scenario" and "scenario unbacked" are the
			// same bag and different phrases.
			add(tokens[i] + " " + tokens[i+1])
		}
	}

	vec := make([]float32, dim)
	for sum, count := range counts {
		// Sublinear term frequency: the tenth occurrence of a word says much
		// less than the second, and untamed counts let one repeated token
		// dominate a chunk's whole direction.
		weight := 1 + math.Log(count)
		if sum>>31&1 == 1 {
			weight = -weight
		}
		vec[int(sum%uint32(dim))] += float32(weight)
	}
	return vec
}

// tokenise lowercases and splits on anything that is not a letter or digit.
func tokenise(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		out = append(out, f)
	}
	return out
}
