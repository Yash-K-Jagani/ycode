package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Benchmarks for the SSE reader and the native tool-call reassembler.
//
// Both run on every streamed chunk of every turn, and the reassembler is the
// subtlest code in the provider layer: it has to key by index, overwrite the name
// and concatenate the arguments, because they arrive by different rules. Three of
// the ways to get that wrong are silent, so the shape of its cost is worth having
// measured rather than assumed.

// chunk builds one SSE data line.
func chunk(delta string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]string{"content": delta}}},
	})
	return "data: " + string(b)
}

func toolChunk(index int, id, name, args string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": index, "id": id,
				"function": map[string]string{"name": name, "arguments": args}},
		}}}},
	})
	return "data: " + string(b)
}

// serve answers with a fixed body, once built, so the measurement is the parser
// rather than the server.
func serve(tb testing.TB, body string) *Client {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	tb.Cleanup(srv.Close)
	return New("testprov", srv.URL, "sk-test")
}
func runOnce(tb testing.TB, c *Client) {
	tb.Helper()
	if _, err := c.StreamWithTools(context.Background(), "m", nil, nil, io.Discard); err != nil {
		tb.Fatal(err)
	}
}

// A short answer is the common case and is what a chat turn pays.
func BenchmarkStreamShortAnswer(b *testing.B) {
	body := sse(chunk("Hello"), chunk(" there"), "data: [DONE]")
	c := serve(b, body)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runOnce(b, c)
	}
}

// A long answer is the interesting one: the reader accumulates the whole thing
// and writes it out, so this is where a quadratic implementation would show.
func BenchmarkStreamLongAnswer(b *testing.B) {
	for _, tokens := range []int{100, 1000, 5000} {
		b.Run(itoa(tokens), func(b *testing.B) {
			var lines []string
			for i := 0; i < tokens; i++ {
				lines = append(lines, chunk("word "))
			}
			lines = append(lines, "data: [DONE]")
			c := serve(b, sse(lines...))
			b.SetBytes(int64(tokens * 5))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				runOnce(b, c)
			}
		})
	}
}

// One chunk carrying a megabyte. A model does not usually do this, but a runaway
// generation does, and the scanner buffer is 1MB, so it is the boundary that
// decides whether the reader works or errors.
func BenchmarkStreamOneHugeChunk(b *testing.B) {
	c := serve(b, sse(chunk(strings.Repeat("x", 1<<20)), "data: [DONE]"))
	b.SetBytes(1 << 20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runOnce(b, c)
	}
}

// Noise the reader must skip: keepalives, blank lines and comments are what a
// real proxy sends, and a reader that mishandles them either errors or drops the
// stream.
func BenchmarkStreamWithKeepalives(b *testing.B) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "", ": keep-alive", chunk("word "))
	}
	lines = append(lines, "data: [DONE]")
	c := serve(b, sse(lines...))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runOnce(b, c)
	}
}

// --- the tool-call reassembler ---

// One call split the way providers actually split it: the name once, the
// arguments in fragments.
func BenchmarkToolCallReassembly(b *testing.B) {
	for _, fragments := range []int{1, 4, 16} {
		b.Run("fragments_"+itoa(fragments), func(b *testing.B) {
			var lines []string
			lines = append(lines, toolChunk(0, "call_1", "read", ""))
			for i := 0; i < fragments; i++ {
				lines = append(lines, toolChunk(0, "", "", `{"path":"internal/tui/turn`))
			}
			lines = append(lines, toolChunk(0, "", "", `.go"}`), "data: [DONE]")
			c := serve(b, sse(lines...))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				runOnce(b, c)
			}
		})
	}
}

// Several calls interleaved, which is the case a single shared buffer gets
// completely wrong.
func BenchmarkParallelToolCalls(b *testing.B) {
	for _, n := range []int{2, 8} {
		b.Run("calls_"+itoa(n), func(b *testing.B) {
			var lines []string
			for i := 0; i < n; i++ {
				lines = append(lines, toolChunk(i, "c", "read", `{"path":"a.go"}`))
			}
			lines = append(lines, "data: [DONE]")
			c := serve(b, sse(lines...))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				runOnce(b, c)
			}
		})
	}
}

// The builder alone, without a server, because it is the part with the subtle
// rules and the part worth profiling directly.
func BenchmarkToolBuilder(b *testing.B) {
	b.Run("many_calls_one_chunk", func(b *testing.B) {
		events := make([][3]string, 64)
		for i := range events {
			events[i] = [3]string{"read", `{"path":"a.go"}`, ""}
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tb := newToolBuilder()
			for j, e := range events {
				tb.add(j, e[2], e[0], e[1])
			}
			if len(tb.calls()) != 64 {
				b.Fatalf("got %d calls", len(tb.calls()))
			}
		}
	})
	b.Run("one_call_many_fragments", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tb := newToolBuilder()
			// One opening fragment, then filler inside the string: the fragments
			// must concatenate into valid JSON, or calls() correctly drops the
			// result and the count check would be measuring nothing.
			tb.add(0, "call_1", "read", `{"path":"`)
			for j := 0; j < 32; j++ {
				tb.add(0, "", "", "fragment ")
			}
			tb.add(0, "", "", `of a long path"}`)
			if len(tb.calls()) != 1 {
				b.Fatalf("got %d calls", len(tb.calls()))
			}
		}
	})
}

// Decoding is separate from the network on purpose: this is the per-chunk JSON
// cost with no server in the way.
func BenchmarkDecodeEvent(b *testing.B) {
	plain := `{"choices":[{"delta":{"content":"a word of output here"}}]}`
	withTool := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}}]}`
	usage := `{"choices":[{"delta":{"content":"x"}}],"usage":{"prompt_tokens":1200,"completion_tokens":340}}`
	for _, bc := range []struct{ name, data string }{
		{"plain_delta", plain},
		{"tool_call_delta", withTool},
		{"usage_delta", usage},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, ok := decodeEvent(bc.data); !ok {
					b.Fatal("did not decode")
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
