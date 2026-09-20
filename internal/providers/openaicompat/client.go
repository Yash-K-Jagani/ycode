package openaicompat

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
	Name_        string
	BaseURL      string
	APIKey       string
	HTTP         *http.Client
	ExtraHeaders map[string]string
}

func New(name, baseURL, key string) *Client {
	return &Client{Name_: name, BaseURL: strings.TrimSuffix(baseURL, "/"), APIKey: key, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Name() string { return c.Name_ }

func (c *Client) ListModels(ctx context.Context) ([]apitypes.ModelInfo, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("%s: API key not set (env var empty)", c.Name_)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	var out []apitypes.ModelInfo
	for _, m := range v.Data {
		out = append(out, apitypes.ModelInfo{ID: m.ID, Provider: c.Name_})
	}
	return out, nil
}

func (c *Client) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	if c.APIKey == "" {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: API key not set", c.Name_)
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "messages": msgs, "stream": true})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apitypes.StreamChunk{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return apitypes.StreamChunk{}, fmt.Errorf("%s %d: %s", c.Name_, resp.StatusCode, string(b))
	}
	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		for _, ch := range ev.Choices {
			full.WriteString(ch.Delta.Content)
			_, _ = io.WriteString(w, ch.Delta.Content)
		}
	}
	return apitypes.StreamChunk{Delta: full.String(), Done: true}, sc.Err()
}

func (c *Client) Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error) {
	var buf bytes.Buffer
	s, err := c.Stream(ctx, model, msgs, &buf)
	return s.Delta, err
}
