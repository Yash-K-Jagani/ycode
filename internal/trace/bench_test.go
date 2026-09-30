package trace

import (
	"github.com/Yash-K-Jagani/ycode/internal/security"
	"strings"
	"testing"
)

// The recorder redacts every record. security.Redact runs seventeen regexes over
// the text, so this is on the path of every tool call in every turn - and it was
// entirely unmeasured, in a package that exists to make debugging possible.
//
// A recorder that costs a millisecond per record would be a reason not to record,
// so the number belongs next to the feature rather than in a comment.

func benchRecorder() *Recorder { return New() }

// The common case: a tool result with no secrets in it. This is the cost a normal
// turn pays, so it is the number that matters.
func BenchmarkRecord(b *testing.B) {
	for _, bc := range []struct {
		name string
		rec  func(*Recorder)
	}{
		{"note_small", func(r *Recorder) { r.Note(0, "something happened") }},
		{"note_long", func(r *Recorder) {
			r.Note(0, strings.Repeat("a line of ordinary output. ", 40))
		}},
		{"tool_call", func(r *Recorder) {
			r.Tool(0, "read", `{"path":"internal/tui/turn.go"}`, "package tui\n\n"+strings.Repeat("// a comment\n", 20), nil)
		}},
		{"request", func(r *Recorder) {
			r.Request(0, "gemini", "gemini-2.5-flash", strings.Repeat("system prompt line. ", 60))
		}},
		// The shape a runaway tool produces, which is the one that must not make
		// tracing itself expensive.
		{"huge_output", func(r *Recorder) {
			r.Tool(0, "bash", `{"command":"go test ./..."}`, strings.Repeat("PASS example/pkg 0.5s\n", 5000), nil)
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			r := benchRecorder()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				bc.rec(r)
				if r.Count() > maxEvents-8 {
					// Keep the ring from dominating, so the measurement is of
					// recording rather than of the copy that evicts the oldest.
					r.Reset()
				}
			}
		})
	}
}

// Redaction scales with the number of secrets present, not just the text length,
// because every rule is tried against every byte. This separates the two.
func BenchmarkRedact(b *testing.B) {
	for _, bc := range []struct {
		name string
		text string
	}{
		// Deliberately free of key/token/secret/password/credential, so the two
		// expensive assignment rules can be skipped. with_secrets_word below is
		// ordinary prose too, but has one trigger word, which is why the skip
		// cannot fire on it - and why the two numbers differ so much.
		{"clean_no_triggers", "the quick brown fox jumps over the lazy dog repeatedly"},
		{"with_secrets_word", "an ordinary sentence with no secrets in it at all"},
		{"clean_1k", strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40)},
		{"clean_1k_with_trigger", strings.Repeat("an ordinary sentence with no secrets. ", 40)},
		{"with_key", "here is my key " + fakeOpenAIKey + " end"},
		{"many_keys", strings.Repeat(fakeOpenAIKey+" ", 20)},
		{"json_args", `{"path":"a.go","content":"` + strings.Repeat("x", 1000) + `","key":"` + fakeAWSKey + `"}`},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				security.Redact(bc.text)
			}
		})
	}
}

// The eviction copy is O(n) and runs whenever the ring is full, which in a long
// turn is every record after the first maxEvents.
func BenchmarkEviction(b *testing.B) {
	r := New()
	for i := 0; i < maxEvents; i++ {
		r.Note(0, "filler")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Note(0, "one more record")
	}
}

// A session-long trace is read, not just written, so the read path is measured
// too. It copies, which is the point - the caller must not be able to mutate the
// recorder's state from another goroutine.
func BenchmarkEventsCopy(b *testing.B) {
	r := New()
	for i := 0; i < 400; i++ {
		r.Note(0, strings.Repeat("line of trace text. ", 5))
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Events()
	}
}

// Rendering is what the user waits for, on a full trace, for every /debug.
func BenchmarkRender(b *testing.B) {
	r := New()
	for i := 0; i < 200; i++ {
		n := r.NextTurn()
		r.Request(n, "gemini", "gemini-2.5-flash", strings.Repeat("prompt line. ", 10))
		r.Tool(n, "read", `{"path":"a.go"}`, strings.Repeat("result line. ", 5), nil)
		r.Response(n, "gemini", "gemini-2.5-flash", strings.Repeat("answer line. ", 10))
	}
	events := r.Events()
	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Render(events, false)
		}
	})
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Render(events, true)
		}
	})
}
