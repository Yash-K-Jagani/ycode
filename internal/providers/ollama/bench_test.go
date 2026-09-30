package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// --- regression ---

// Ollama's stream was a bufio.Scanner, whose 1MB line limit surfaces as "token
// too long". openaicompat had the identical bug and it cost a whole turn.
//
// Unlike openaicompat, Ollama checked sc.Err(), so it reported an error rather
// than silently truncating - but the error was "ollama: reading stream: token
// too long", which is indistinguishable from a dropped connection, and the user
// still lost the turn.
//
// Reachable in practice: a base64 image part, or a large tool-call argument, is
// enough. NDJSON does not guarantee small lines the way a token-per-line habit
// suggests.
func TestStreamHandlesLineOverScannerLimit(t *testing.T) {
	const big = 2 << 20 // comfortably over the old 1MB Scanner limit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"before "},"done":false}`+"\n")
		// One NDJSON line holding a 2MB argument blob.
		_, _ = fmt.Fprintf(w, `{"message":{"tool_calls":[{"function":{"name":"write","arguments":{"content":%q}}}]}}`+"\n",
			strings.Repeat("x", big))
		_, _ = fmt.Fprint(w, `{"message":{"content":"after"},"done":true,"prompt_eval_count":11,"eval_count":22}`+"\n")
	}))
	defer srv.Close()

	var out strings.Builder
	got, err := New(srv.URL).Stream(context.Background(), "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "hi"}}, &out)
	if err != nil {
		t.Fatalf("a 2MB line is not a network failure: %v", err)
	}
	if !strings.Contains(got.Delta, "after") {
		t.Fatalf("output after the large line was lost: %q", got.Delta)
	}
	if len(got.ToolCalls) != 1 || len(got.ToolCalls[0].Args) == 0 {
		t.Fatalf("tool call over the old limit was lost: %+v", got.ToolCalls)
	}
	if got.PromptTok != 11 || got.ComplTok != 22 {
		t.Fatalf("usage lost: %d/%d", got.PromptTok, got.ComplTok)
	}
}

// A stream that ends without a trailing newline is normal, not a truncation
// error, and the final line must still be decoded.
func TestStreamAcceptsUnterminatedFinalLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"a"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"content":"b"},"done":true,"eval_count":3}`)
	}))
	defer srv.Close()

	var out strings.Builder
	got, err := New(srv.URL).Stream(context.Background(), "m", nil, &out)
	if err != nil {
		t.Fatalf("unterminated final line should not be an error: %v", err)
	}
	if got.Delta != "ab" {
		t.Fatalf("final line lost: %q", got.Delta)
	}
}

// --- benchmarks ---

// ndjsonLines builds an Ollama-style token-per-line stream. This is the shape
// Ollama actually produces, and it is nothing like SSE: one JSON object per
// newline, no "data:" prefix, no blank lines to skip, and tool arguments as
// objects rather than JSON-encoded strings.
func ndjsonLines(lines, contentPerLine int, withTools bool) string {
	var b strings.Builder
	arg := strings.Repeat("a", 64)
	for i := range lines {
		switch {
		case withTools && i == 0:
			fmt.Fprintf(&b,
				`{"message":{"tool_calls":[{"function":{"name":"bash","arguments":{"command":%q}}}]}}`+"\n",
				arg)
		case i == lines-1:
			fmt.Fprintf(&b, `{"message":{"content":%q},"done":true,"prompt_eval_count":900,"eval_count":%d}`+"\n",
				strings.Repeat("z", contentPerLine), lines)
		default:
			fmt.Fprintf(&b, `{"message":{"content":%q},"done":false}`+"\n",
				strings.Repeat("z", contentPerLine))
		}
	}
	return b.String()
}

func benchStream(b *testing.B, body string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	b.Cleanup(srv.Close)
	c := New(srv.URL)
	msgs := []apitypes.Message{{Role: apitypes.RoleUser, Content: "hi"}}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		var out strings.Builder
		if _, err := c.Stream(context.Background(), "m", msgs, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// One line per token is the common case, so per-line decode cost is what
// actually matters here - unlike SSE, where cost is per-event regardless of how
// many tokens arrive inside one frame.
func BenchmarkStreamNDJSON(b *testing.B) {
	for _, n := range []int{1, 50, 500, 2000} {
		b.Run(fmt.Sprintf("lines_%d", n), func(b *testing.B) {
			benchStream(b, ndjsonLines(n, 8, false))
		})
	}
}

// A delta of realistic width, to separate per-line cost from per-byte cost.
func BenchmarkStreamNDJSONWideDeltas(b *testing.B) {
	for _, n := range []int{50, 500} {
		b.Run(fmt.Sprintf("lines_%d_x512B", n), func(b *testing.B) {
			benchStream(b, ndjsonLines(n, 512, false))
		})
	}
}

// Ollama sends object arguments where OpenAI sends JSON strings, so the idless
// collector merges by index rather than by id.
func BenchmarkStreamNDJSONWithToolCalls(b *testing.B) {
	benchStream(b, ndjsonLines(200, 8, true))
}

const schema = `{"type":"object","properties":{"a":{"type":"string"}}}`

// toWire runs once per request over the prompt's full history, so it is the cost
// that grows with the conversation.
func BenchmarkToWire(b *testing.B) {
	for _, n := range []int{1, 20, 200} {
		b.Run(fmt.Sprintf("messages_%d", n), func(b *testing.B) {
			msgs := make([]apitypes.Message, n)
			for i := range msgs {
				msgs[i] = apitypes.Message{Role: apitypes.RoleUser, Content: strings.Repeat("word ", 40)}
			}
			b.ReportAllocs()
			for range b.N {
				if len(toWire(msgs)) == 0 {
					b.Fatal("empty")
				}
			}
		})
	}
}

// Tool schemas are re-sent in full on every request, and the default registry is
// 30 tools - so this is the single largest constant on the wire for a turn.
func BenchmarkToolParams(b *testing.B) {
	for _, n := range []int{1, 10, 30} {
		b.Run(fmt.Sprintf("specs_%d", n), func(b *testing.B) {
			specs := make([]apitypes.ToolSpec, n)
			for i := range specs {
				specs[i] = apitypes.ToolSpec{
					Name:        fmt.Sprintf("tool_%d", i),
					Description: "does a thing",
					Schema:      schema,
				}
			}
			b.ReportAllocs()
			for range b.N {
				// toolParams returns the envelope, so count the tools inside it -
				// otherwise the benchmark would assert on a constant 1 and prove
				// nothing about the specs it was built from.
				got, ok := toolParams(specs)["tools"].([]map[string]any)
				if !ok || len(got) != n {
					b.Fatalf("want %d tools, got %d", n, len(got))
				}
			}
		})
	}
}
