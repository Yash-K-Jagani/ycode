package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/httpx"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Native tool calling.
//
// Without this, a model is asked to call a tool by being shown its schema in the
// system prompt and then writing `<tool:read>{"path":"x.go"}</tool:read>` into
// its answer, which the harness parses back out. That works with small local
// models and it works by accident: the schema is prose, so the model has to
// reproduce it from memory, and a tool whose arguments run long is a tool that
// gets truncated or hallucinated.
//
// Native tool calling moves the schema into the request and the call back out as
// structured data, which is what the model was trained to do. Small models
// follow it considerably better than they follow a tag they were never taught.

// toolBuilder reassembles tool calls from a stream.
//
// The wire format is the awkward part. A call arrives split across many chunks,
// and what comes in each is a *fragment* keyed by index:
//
//	{"index":0,"id":"call_a","function":{"name":"read","arguments":""}}
//	{"index":0,"function":{"arguments":"{\"pa"}}
//	{"index":0,"function":{"arguments":"th\":\"main.go\"}"}}
//
// Two mistakes are easy and both are silent. Treating each chunk as a whole call
// yields three calls with truncated JSON. Concatenating everything into one
// buffer yields one call whose name is the first fragment repeated. So the
// builder keys by index and keeps each call's name and arguments separately,
// because they arrive by different rules: the name arrives whole and once, the
// arguments arrive in arbitrary pieces.
//
// A missing index is treated as the single call in flight. Several servers omit
// it for the common case of exactly one tool call, and rejecting that would
// break the majority of requests over a field that is optional in practice.
type toolBuilder struct {
	order []int
	parts map[int]*toolCallBuilder
}

type toolCallBuilder struct {
	id   string
	name string
	args strings.Builder
}

func newToolBuilder() *toolBuilder {
	return &toolBuilder{parts: map[int]*toolCallBuilder{}}
}

// add folds one streamed tool-call fragment in.
//
// fragment carries the already-unmarshalled fields; index is the position the
// server assigned, or -1 when it sent none.
func (b *toolBuilder) add(index int, id, name, args string) {
	if index < 0 {
		// A single unindexed call: reuse whatever is in flight rather than
		// creating a second one per chunk.
		if len(b.order) > 0 {
			index = b.order[len(b.order)-1]
		} else {
			index = 0
		}
	}
	c := b.parts[index]
	if c == nil {
		c = &toolCallBuilder{}
		b.parts[index] = c
		b.order = append(b.order, index)
	}
	// Last writer wins for the scalar fields. A server that repeats the name on
	// every fragment sends the same value, and one that sends it once sends
	// nothing afterwards; neither case is harmed by overwriting.
	if id != "" {
		c.id = id
	}
	if name != "" {
		c.name = name
	}
	// Arguments concatenate, because each chunk is a fragment of one JSON
	// document rather than a replacement for it.
	c.args.WriteString(args)
}

// calls returns the assembled calls in index order.
//
// Entries with no name are dropped: a fragment that arrived without the function
// name is not a tool call, and handing the harness one would produce a call to
// the empty tool, which then fails with a confusing "unknown tool" error instead
// of the real problem.
func (b *toolBuilder) calls() []apitypes.ToolCall {
	// Nil when there were none, not an empty slice.
	//
	// StreamChunk documents nil as "the model produced prose" and reserves
	// empty-but-non-nil for "it asked for something we could not read". Returning
	// make(..., 0, n) would collapse those two into one, and a caller deciding
	// whether to fall back to the text protocol needs to tell them apart.
	if len(b.order) == 0 {
		return nil
	}
	out := make([]apitypes.ToolCall, 0, len(b.order))
	for _, idx := range b.order {
		c := b.parts[idx]
		if c == nil || c.name == "" {
			continue
		}
		call := apitypes.ToolCall{ID: c.id, Name: c.name}
		raw := strings.TrimSpace(c.args.String())
		if raw != "" {
			// Validated here rather than at execution time, so a malformed
			// call fails with the model's own text attached instead of a decode
			// error naming an internal type.
			if !json.Valid([]byte(raw)) {
				continue
			}
			call.Args = json.RawMessage(raw)
		}
		out = append(out, call)
	}
	return out
}

// toolParams is the request body extension: what the model is told it may call.
//
// An empty slice must produce no "tools" key at all rather than an empty array.
// Several servers reject the empty array outright, and sending it would mean a
// provider that supports tool calling quietly broke every request from a user
// who has no tools enabled.
func toolParams(specs []apitypes.ToolSpec) map[string]any {
	if len(specs) == 0 {
		return nil
	}
	tools := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		fn := map[string]any{"name": s.Name}
		if s.Description != "" {
			fn["description"] = s.Description
		}
		if s.Schema != "" {
			fn["parameters"] = s.Schema
		}
		tools = append(tools, map[string]any{"type": "function", "function": fn})
	}
	return map[string]any{"tools": tools, "tool_choice": "auto"}
}

// StreamWithTools runs a turn with native tool calling.
//
// Stream delegates here with no specs, so there is exactly one implementation of
// the request, the retry policy, the idle watchdog and the error messages. Two
// copies of that is how the two paths start disagreeing about what a provider
// error looks like.
func (c *Client) StreamWithTools(ctx context.Context, model string, msgs []apitypes.Message, specs []apitypes.ToolSpec, w io.Writer) (apitypes.StreamChunk, error) {
	if c.APIKey == "" {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: API key not set", c.Name_)
	}
	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	for k, v := range toolParams(specs) {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: encoding request: %w", c.Name_, err)
	}
	req, err := httpx.NewRequest(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: %w", c.Name_, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	// Retried only while nothing has been received. Once the first chunk has
	// been written to the caller's writer the response is committed, and a retry
	// would append a second, different answer to the same transcript.
	resp, err := httpx.DoWithRetry(ctx, c.HTTP, req, httpx.DefaultRetry())
	if err != nil {
		return apitypes.StreamChunk{}, fmt.Errorf("%s: %w", c.Name_, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return apitypes.StreamChunk{}, fmt.Errorf("%s %d: %s", c.Name_, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	streamBody, release, watch := httpx.WithStreamIdle(streamCtx, cancelStream, resp.Body, httpx.StreamIdleTimeout)
	defer release()

	var (
		full       strings.Builder
		promptTok  int
		complTok   int
		events     int
		decoded    int
		firstBad   string
		sawData    bool
		bodyBytes  int
		tb         = newToolBuilder()
		sc         = bufio.NewScanner(streamBody)
		chunkLimit = 1024 * 1024
	)
	sc.Buffer(make([]byte, 1024), chunkLimit)
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
		ev, ok := decodeEvent(data)
		if !ok {
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
			for _, tc := range ch.Delta.ToolCalls {
				tb.add(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if watch.Fired() {
			return apitypes.StreamChunk{Delta: full.String()}, fmt.Errorf(
				"%s: the provider stopped sending data for %s; the connection was closed",
				c.Name_, httpx.StreamIdleTimeout)
		}
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
		ToolCalls: tb.calls(),
	}, nil
}
