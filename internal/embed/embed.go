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
	return &Client{Host: strings.TrimSuffix(host, "/"), Model: model, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": c.Model, "input": inputs})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.Host+"/api/embed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w (is ollama up? pull %s)", err, c.Model)
	}
	defer resp.Body.Close()
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

func Cosine(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
