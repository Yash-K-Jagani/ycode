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

	"github.com/Yash-K-Jagani/ycode/internal/httpx"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
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
	// The shared stream client rather than a private one with a 10-minute
	// blanket timeout. A local model on modest hardware is the case most likely
	// to exceed ten minutes - a 7B generating a long answer - and the blanket
	// timeout killed exactly that, mid-sentence, discarding the partial output.
	// The phases are bounded by the transport instead, and the body is watched
	// for stalls, which is what actually detects a wedged daemon.
	return &Client{Host: strings.TrimSuffix(host, "/"), HTTP: httpx.StreamClient()}
}

func (c *Client) Name() string { return "ollama" }

func (c *Client) ListModels(ctx context.Context) ([]apitypes.ModelInfo, error) {
	req, err := httpx.NewRequest(ctx, "GET", c.Host+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
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
	req, err := httpx.NewRequest(ctx, "POST", c.Host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Retried only while nothing has arrived. A retry after the first chunk
	// would append a second answer to the same writer.
	// http.NewRequestWithContext gives GetBody for a *bytes.Reader, so the
	// payload can be re-sent on each attempt.
	resp, err := httpx.DoWithRetry(ctx, c.HTTP, req, httpx.DefaultRetry())
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return apitypes.StreamChunk{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}

	// A wedged daemon holds the connection open and stops sending, which is
	// indistinguishable from a slow model without a watchdog. Cancelling the
	// request context is what interrupts the blocked read.
	streamCtx, cancelStream := context.WithCancel(ctx)
	streamBody, release, watch := httpx.WithStreamIdle(streamCtx, cancelStream, resp.Body, httpx.StreamIdleTimeout)
	defer release()

	var full strings.Builder
	var promptTok, complTok int
	// See openaicompat: a line that will not parse used to be skipped, so a
	// response the harness could not read came back as a successful empty
	// answer and the user was told the model had nothing to say.
	var lines, decoded int
	var firstBad string
	sc := bufio.NewScanner(streamBody)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines++
		var line struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
			// Ollama reports real token counts on its final chunk, so the
			// harness can show a measured number instead of len/4.
			PromptEvalCount int `json:"prompt_eval_count"`
			EvalCount       int `json:"eval_count"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			if firstBad == "" {
				firstBad = textutil.Truncate(strings.TrimSpace(sc.Text()), 200)
			}
			continue
		}
		decoded++
		if line.PromptEvalCount > 0 {
			promptTok = line.PromptEvalCount
		}
		if line.EvalCount > 0 {
			complTok = line.EvalCount
		}
		if line.Message.Content != "" {
			full.WriteString(line.Message.Content)
			_, _ = io.WriteString(w, line.Message.Content)
		}
		if line.Done {
			break
		}
	}
	if err := sc.Err(); err != nil {
		// A daemon that stops sending is otherwise indistinguishable from a
		// slow model, and the user needs to be told which they are looking at.
		if watch.Fired() {
			return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf(
				"ollama: no data for %s; the daemon may have stopped mid-generation",
				httpx.StreamIdleTimeout)
		}
		return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf("ollama: reading stream: %w", err)
	}
	if lines > 0 && decoded == 0 {
		return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf(
			"ollama: could not read the response (%d lines, none parsed; first was: %s)",
			lines, firstBad)
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
	if err != nil {
		return "", err
	}
	return s.Delta, nil
}
