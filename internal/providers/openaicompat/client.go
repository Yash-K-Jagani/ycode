package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/httpx"
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
	// The shared stream client, not a fresh one per provider construction: a
	// Transport owns the connection pool, and per-turn clients meant a new pool
	// per turn, so nearly every request paid a fresh connect and TLS handshake
	// to a host it had just been talking to. It also has no blanket Timeout,
	// which used to kill a long generation at exactly ten minutes with the
	// partial answer discarded. The phases are bounded instead, and the body is
	// watched for stalls by the caller.
	return &Client{Name_: name, BaseURL: strings.TrimSuffix(baseURL, "/"), APIKey: key, HTTP: httpx.StreamClient()}
}

func (c *Client) Name() string { return c.Name_ }

func (c *Client) ListModels(ctx context.Context) ([]apitypes.ModelInfo, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("%s: API key not set (env var empty)", c.Name_)
	}
	req, err := httpx.NewRequest(ctx, "GET", c.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Name_, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Without this, a 401 decodes as an empty catalogue and the user is told
	// they have no models, when the truth is that their key was rejected.
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%s %d: %s", c.Name_, resp.StatusCode, strings.TrimSpace(string(b)))
	}
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

// streamEvent is one decoded SSE payload.
//
// Shared by both paths so the two cannot disagree about what a chunk contains.
// Adding ToolCalls here rather than in a parallel struct is what keeps a future
// fix to the usage handling from having to be made twice.
type streamEvent struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// ToolCalls arrive split across chunks. Index is the position the
			// server assigned; -1 stands in for a missing index, which several
			// servers omit when there is only ever one call.
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// decodeEvent parses one SSE payload, reporting whether it was readable.
func decodeEvent(data string) (streamEvent, bool) {
	var ev streamEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return streamEvent{}, false
	}
	return ev, true
}

// Stream runs a turn with no tools offered.
//
// It delegates rather than duplicating: the request, the retry policy, the idle
// watchdog and the error messages all live in StreamWithTools, and two copies
// of those is how two code paths start disagreeing about what a provider error
// looks like.
func (c *Client) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	return c.StreamWithTools(ctx, model, msgs, nil, w)
}

func (c *Client) Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error) {
	var buf bytes.Buffer
	s, err := c.Stream(ctx, model, msgs, &buf)
	return s.Delta, err
}
