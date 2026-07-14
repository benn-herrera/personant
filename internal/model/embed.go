package model

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"personant/internal/memops"
)

// embedBatch caps how many texts go in one /embeddings request.
// OpenAI-compatible servers accept an input array; this keeps any one
// request bounded.
const embedBatch = 64

// NewHTTPEmbedder constructs an Embedder backed by the provider's
// OpenAI-compatible /embeddings endpoint. The embedding model is a
// config.toml choice (not a provider property), so it is passed
// explicitly; dimensions > 0 requests a Matryoshka-truncated vector of
// that length (0 → the model's native dimension). The same *HTTPClient
// type satisfies both Client and Embedder.
func NewHTTPEmbedder(p memops.Provider, model string, dimensions int) Embedder {
	c := NewHTTPClient(p).(*HTTPClient)
	c.embeddingModel = model
	c.embeddingDimensions = dimensions
	return c
}

// Embed implements Embedder against the provider's /embeddings
// endpoint, using the model set by NewHTTPEmbedder. Texts are sent in
// batches of embedBatch; the returned vectors are in input order.
func (c *HTTPClient) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if c.provider.BaseURL == "" {
		return nil, fmt.Errorf("provider BaseURL is empty")
	}
	if c.embeddingModel == "" {
		return nil, fmt.Errorf("no embedding model configured")
	}
	out := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatch {
		end := min(start+embedBatch, len(texts))
		vecs, err := c.embedOne(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

type wireEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// Dimensions requests a Matryoshka-truncated vector; omitted when 0.
	Dimensions int `json:"dimensions,omitempty"`
}

type wireEmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// embedOne performs one /embeddings round-trip for a single batch.
func (c *HTTPClient) embedOne(ctx context.Context, texts []string) ([][]float64, error) {
	body, err := json.Marshal(wireEmbedRequest{
		Model:      c.embeddingModel,
		Input:      texts,
		Dimensions: c.embeddingDimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "embeddings", "application/json", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}

	var wire wireEmbedResponse
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(wire.Data) != len(texts) {
		return nil, fmt.Errorf("embed response: got %d vectors for %d inputs",
			len(wire.Data), len(texts))
	}
	// The endpoint should return data in input order, but the `index`
	// field is authoritative — sort by it to be safe.
	sort.Slice(wire.Data, func(i, j int) bool { return wire.Data[i].Index < wire.Data[j].Index })
	out := make([][]float64, len(wire.Data))
	for i, d := range wire.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// MockEmbedder is a deterministic Embedder for tests. It is a feature-
// hashing vectorizer: each whitespace/punctuation-delimited token of a
// text increments a hashed dimension, and the vector is L2-normalized.
//
// This is not a semantic model, but it has the one property tests of
// embedding recall need: texts sharing tokens get non-zero cosine
// similarity, and identical text always yields the identical vector.
// Real embedding quality is measured separately against a live model.
type MockEmbedder struct {
	dim int
}

// NewMockEmbedder returns a MockEmbedder with a 256-dimension space.
func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{dim: 256}
}

// Embed implements Embedder deterministically — no I/O, no error.
func (m *MockEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		v := make([]float64, m.dim)
		for _, tok := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
		}) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			v[h.Sum32()%uint32(m.dim)] += 1
		}
		out[i] = l2normalize(v)
	}
	return out, nil
}

// l2normalize scales v to unit length. A zero vector is returned
// unchanged (cosine with it is 0, the correct "no signal" result).
func l2normalize(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return v
	}
	norm := math.Sqrt(sum)
	for i := range v {
		v[i] /= norm
	}
	return v
}
