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

	"github.com/Yash-K-Jagani/ycode/internal/httpx"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
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

func (c *Client) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	if c.APIKey == "" {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: API key not set", c.Name_)
	}
	// include_usage asks the server to send a final chunk carrying token
	// counts. Without it the harness has to guess tokens as len/4, which is
	// how a cost figure ends up on screen looking measured when it is not.
	payload, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	})
	req, err := httpx.NewRequest(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: %w", c.Name_, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apitypes.StreamChunk{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return apitypes.StreamChunk{}, fmt.Errorf("%s %d: %s", c.Name_, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var full strings.Builder
	var promptTok, complTok int
	// A data line that will not parse used to be skipped silently, so a
	// response the harness could not read at all came back as a successful
	// empty answer and the user was told the model had nothing to say. The
	// counters turn "every line failed" into an error naming the real cause.
	var events, decoded int
	var firstBad string
	// sawData distinguishes "the server sent an event stream I could not read"
	// from "the server did not send an event stream at all". A proxy or SSO
	// login page is HTML with no data: lines anywhere, and it used to come
	// back as a successful empty answer.
	var sawData bool
	bodyBytes := 0
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			bodyBytes += len(line)
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		sawData = true
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		events++
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			if firstBad == "" {
				firstBad = textutil.Truncate(strings.TrimSpace(data), 200)
			}
			continue
		}
		decoded++
		if ev.Usage != nil {
			promptTok = ev.Usage.PromptTokens
			complTok = ev.Usage.CompletionTokens
		}
		for _, ch := range ev.Choices {
			full.WriteString(ch.Delta.Content)
			_, _ = io.WriteString(w, ch.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf("%s: reading stream: %w", c.Name_, err)
	}
	if bodyBytes > 0 && !sawData {
		return apitypes.StreamChunk{}, fmt.Errorf(
			"%s: the response was not an event stream (%d bytes, no data: lines) - "+
				"this usually means a proxy, gateway or login page answered instead of the API",
			c.Name_, bodyBytes)
	}
	if events > 0 && decoded == 0 {
		return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf(
			"%s: could not read the response (%d events, none parsed; first was: %s) - "+
				"the endpoint may not speak the OpenAI chat-completions stream format",
			c.Name_, events, firstBad)
	}
	return apitypes.StreamChunk{
		Delta:     full.String(),
		Done:      true,
		PromptTok: promptTok,
		ComplTok:  complTok,
	}, nil
}

func (c *Client) Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error) {
	var buf bytes.Buffer
	s, err := c.Stream(ctx, model, msgs, &buf)
	return s.Delta, err
}
