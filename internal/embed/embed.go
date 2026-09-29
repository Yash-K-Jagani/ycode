package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/httpx"
)

type Client struct {
	Host  string
	Model string
	HTTP  *http.Client
}

func New(host, model string) *Client {
	if host == "" {
		host = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &Client{Host: strings.TrimSuffix(host, "/"), Model: model, HTTP: httpx.Client(5 * time.Minute)}
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": c.Model, "input": inputs})
	req, err := httpx.NewRequest(ctx, "POST", c.Host+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w (is ollama up? pull %s)", err, c.Model)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("embed %d: %s (try: ollama pull %s)", resp.StatusCode, string(b), c.Model)
	}
	var v struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return v.Embeddings, nil
}

// Cosine returns the cosine similarity of two vectors.
//
// Vectors of different lengths are an error rather than a truncated
// comparison. They mean the two were produced by different embedding models -
// nomic-embed-text is 768 dimensions, others are not - and comparing the
// overlapping prefix yields a confident, plausible score computed from
// unrelated numbers. A caller that silently ranks an incompatible index
// returns wrong answers with no indication anything is wrong, which is worse
// than being told to re-index.
//
// A zero vector has no direction, so its similarity is 0 with no error: that
// is a real value, not a mismatch.
func Cosine(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("embed: cannot compare %d-dimensional and %d-dimensional vectors: the embedding model changed, re-index with /rag ingest", len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}
