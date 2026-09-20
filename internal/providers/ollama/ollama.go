package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Client struct {
	Host string
	HTTP *http.Client
}

func New(host string) *Client {
	if host == "" {
		host = "http://localhost:11434"
	}
	return &Client{Host: strings.TrimSuffix(host, "/"), HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Name() string { return "ollama" }

func (c *Client) ListModels(ctx context.Context) ([]apitypes.ModelInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.Host+"/api/tags", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama not reachable at %s: %w", c.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	var out []apitypes.ModelInfo
	for _, m := range v.Models {
		out = append(out, apitypes.ModelInfo{ID: m.Name, Provider: "ollama", Status: "installed"})
	}
	return out, nil
}

func (c *Client) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "messages": msgs, "stream": true})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.Host+"/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apitypes.StreamChunk{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return apitypes.StreamChunk{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}
	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var line struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.Message.Content != "" {
			full.WriteString(line.Message.Content)
			_, _ = io.WriteString(w, line.Message.Content)
		}
		if line.Done {
			break
		}
	}
	_ = full.String()
	return apitypes.StreamChunk{Delta: full.String(), Done: true}, sc.Err()
}

func (c *Client) Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error) {
	var buf bytes.Buffer
	s, err := c.Stream(ctx, model, msgs, &buf)
	if err != nil {
		return "", err
	}
	return s.Delta, nil
}
