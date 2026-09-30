package ollama

import (
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

// Stream runs a turn with no tools offered.
//
// It delegates rather than duplicating: the retry policy, the idle watchdog and
// the error messages all live in StreamWithTools, and two copies of those is how
// two paths start disagreeing about what a daemon error looks like.
func (c *Client) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	return c.StreamWithTools(ctx, model, msgs, nil, w)
}

// StreamWithTools runs a turn, optionally offering tool schemas.
//
// This is where the local models most need it. Small models are the ones that
// follow a schema poorly when it is prose in a system prompt, and they are also
// the ones most likely to truncate a long <tool:read>{"path":...}</tool:read>
// tag. The structured form removes both failure modes.
func (c *Client) StreamWithTools(ctx context.Context, model string, msgs []apitypes.Message, specs []apitypes.ToolSpec, w io.Writer) (apitypes.StreamChunk, error) {
	body := map[string]any{
		"model":    model,
		"messages": toWire(msgs),
		"stream":   true,
	}
	for k, v := range toolParams(specs) {
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("ollama: encoding request: %w", err)
	}
	req, err := httpx.NewRequest(ctx, "POST", c.Host+"/api/chat", bytes.NewReader(encoded))
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
	calls := newCallCollector()
	// httpx.LineReader rather than a bufio.Scanner, for the same reason as
	// openaicompat: the Scanner's 1MB line limit surfaces as "token too long",
	// which is indistinguishable from a dropped connection.
	//
	// Ollama was the milder case - it checked sc.Err(), so it reported an error
	// instead of silently truncating - but the user still lost the turn and was
	// told they were "reading stream". A base64 image part or a large tool-call
	// argument is enough to reach the limit, and NDJSON does not guarantee small
	// lines the way a token-per-line habit suggests.
	lr := httpx.NewLineReader(streamBody, httpx.DefaultMaxLineBytes)
	var readErr error
	for readErr == nil {
		var raw string
		raw, readErr = lr.Next()
		// A stream that ends without a trailing newline is normal, not an error,
		// and the final line still has to be decoded.
		if raw == "" {
			if readErr != nil && readErr != io.EOF {
				readErr = fmt.Errorf("ollama: reading stream: %w", readErr)
			} else {
				readErr = io.EOF
			}
			break
		}
		lines++
		var line struct {
			Message struct {
				Content string `json:"content"`
				// Ollama's tool-call shape. arguments is an object, unlike
				// OpenAI's string, which is the whole reason this provider needs
				// its own translation.
				ToolCalls []struct {
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			Done bool `json:"done"`
			// Ollama reports real token counts on its final chunk, so the
			// harness can show a measured number instead of len/4.
			PromptEvalCount int `json:"prompt_eval_count"`
			EvalCount       int `json:"eval_count"`
		}
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			if firstBad == "" {
				firstBad = textutil.Truncate(strings.TrimSpace(raw), 200)
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
		for i, tc := range line.Message.ToolCalls {
			calls.add(i, apitypes.ToolCall{Name: tc.Function.Name, Args: tc.Function.Arguments})
		}
		if line.Done {
			break
		}
	}
	if readErr != nil && readErr != io.EOF {
		// A daemon that stops sending is otherwise indistinguishable from a
		// slow model, and the user needs to be told which they are looking at.
		if watch.Fired() {
			return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf(
				"ollama: no data for %s; the daemon may have stopped mid-generation",
				httpx.StreamIdleTimeout)
		}
		return apitypes.StreamChunk{Delta: full.String()}, readErr
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
		ToolCalls: calls.calls(),
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
