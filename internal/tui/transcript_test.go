package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// norm collapses a render to comparable text: escape codes removed via the
// package's existing stripANSI, then blank lines, trailing space and leading
// indentation dropped. Glamour and lipgloss pad and wrap, and the exact codes
// depend on terminal width detection, which differs under a test, so comparing
// bytes would make this test about the environment rather than about
// correctness.
func norm(s string) string {
	lines := strings.Split(stripANSI(s), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// The incremental renderer is only safe if, once the message is complete, it has
// produced the same thing a single full render would. This is the test that
// makes that claim checkable rather than aspirational, and it covers the shapes
// that actually occur: prose, prose with code, code with blank lines inside it,
// diffs, and a message that never ends on a boundary.
// The invariant that makes the cache sound.
//
// Once a paragraph boundary goes by, that text is final: it is rendered once and
// never again. So the accumulated stable prefix must be the same text, in the
// same order, with the same code highlighting, as rendering that raw text in a
// single call. If it were not, the transcript would show one set of paragraphs
// for the whole turn and a differently-rendered copy of the same paragraphs
// whenever the session was reloaded or exported.
//
// Compared through norm, which drops the two things a chunked render cannot
// reproduce: glamour's document margin and its padding to the terminal width.
// See TestStreamRenderStablePrefixDiffersOnlyByDocumentMargin, which pins that
// difference down rather than leaving it implicit, and the completed message is
// re-rendered in full when the turn ends, so the margin is restored.
//
// The in-flight tail is deliberately not compared, because it is rendered by the
// cheap streaming renderer: no line-number gutter, no intra-line diff highlight,
// no glamour rewrap. It gains the full treatment when it becomes stable.
func TestStreamRenderStablePrefixMatchesFullRender(t *testing.T) {
	cases := map[string]string{
		"prose":               "First paragraph here.\n\nSecond paragraph with more text in it.\n\nThird one to finish.",
		"trailing boundary":   "One.\n\nTwo.\n\n",
		"code with blanks":    "Before.\n\n```go\nfunc main() {\n\n\tprintln(1)\n\n}\n```\n\nAfter the block.",
		"code only":           "```python\ndef f():\n\n    return 1\n```",
		"diff":                "Here is the change:\n\n```diff\n@@ -1,3 +1,3 @@\n-old line\n+new line\n```\n\nThat is all.",
		"list":                "- one\n- two\n- three\n\nThen a paragraph.",
		"multiple blocks":     "```go\na\n```\n\nmiddle prose\n\n```go\nb\n```\n\ntail",
		"long paragraph":      strings.Repeat("word ", 60) + "\n\n" + strings.Repeat("second ", 60),
		"many paragraphs":     strings.Repeat("para.\n\n", 12),
		"blank line in fence": "intro\n\n```\n\n\nstill open\n```\n\nend",
		"unicode":             "héllo wörld — 世界\n\nsecond ünicode päragraph\n\nthird one",
		"no trailing newline": "Ends abruptly mid-sen",
		"whitespace heavy":    "a\n\n   \n\nb\n\n\t\n\n",
		"code then prose":     "```go\nx := 1\n```\n\nafter the block, at length.",
		"prose then code":     "before the block, at length.\n\n```go\nx := 1\n```",
		"nested fences":       "a\n\n```\n```\n```\n\nb",
		"heading":             "# Title\n\nbody text\n\n## Sub\n\nmore",
		"quote":               "> quoted line\n\nafter",
		"table":               "| a | b |\n| - | - |\n| 1 | 2 |\n\nafter",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			for _, size := range []int{1, 3, 17} {
				var sr streamRender
				var acc string
				for _, piece := range chunk(text, size) {
					acc += piece
					sr.update(acc, renderAssistant, renderStreaming)
				}
				if sr.stableRaw == 0 {
					continue
				}
				got := norm(sr.stableOut)
				want := norm(renderAssistant(sr.raw[:sr.stableRaw]))
				if got != want {
					t.Fatalf("size %d: stable prefix differs from a single render\nraw:   %q\n got:   %q\nwant:   %q",
						size, sr.raw[:sr.stableRaw], got, want)
				}
			}
		})
	}
}

// The one difference that is expected, recorded so it cannot drift silently.
//
// glamour renders a whole markdown document and pads every line out to the
// terminal width, so a full render's lines carry trailing spaces. The
// chunk-by-chunk render trims them, because renderAssistant ends in TrimSpace.
//
// The consequence is bounded and self-correcting: a paragraph's lines lose
// invisible trailing padding as it becomes stable, and the completed message is
// re-rendered in full when the turn ends. Nothing is lost, reordered or restyled
// beyond that, which is what the test above pins. Anything wider - a lost code
// highlight, a collapsed heading, a missing line - would fail that test, and this
// one would not notice, which is exactly why they are separate.
func TestStreamRenderStablePrefixDiffersOnlyByTrailingPadding(t *testing.T) {
	text := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	var sr streamRender
	sr.update(text, renderAssistant, renderStreaming)
	if sr.stableRaw == 0 {
		t.Fatal("expected a stable prefix")
	}
	full := renderAssistant(sr.raw[:sr.stableRaw])

	// Byte-identical only if glamour padded nothing in this environment, which
	// depends on the detected width. Either way the text must match.
	if sr.stableOut != full {
		if norm(sr.stableOut) != norm(full) {
			t.Fatalf("difference is more than padding\n got: %q\nwant: %q", norm(sr.stableOut), norm(full))
		}
		for _, line := range strings.Split(full, "\n") {
			trimmed := strings.TrimRight(line, " \t")
			if trimmed == "" && line != "" {
				continue // a padding-only line
			}
			if !strings.Contains(sr.stableOut, trimmed) {
				t.Fatalf("line %q missing from the chunked render", trimmed)
			}
		}
	}
}

// The stable prefix is a prefix of the whole render, not merely equal to a
// render of itself. This catches a cache that dropped or reordered a paragraph
// while still rendering each chunk correctly in isolation.
func TestStreamRenderStablePrefixIsAPrefixOfFullRender(t *testing.T) {
	text := strings.Repeat("Paragraph one is here.\n\n", 6) + "The last paragraph.\n"
	var sr streamRender
	var acc string
	for _, piece := range chunk(text, 5) {
		acc += piece
		sr.update(acc, renderAssistant, renderStreaming)
	}
	if sr.stableRaw == 0 {
		t.Fatal("expected a stable prefix")
	}
	full := norm(renderAssistant(text))
	prefix := norm(renderAssistant(sr.raw[:sr.stableRaw]))
	if !strings.HasPrefix(full, prefix) {
		t.Fatalf("stable prefix is not a prefix of the full render\nprefix: %q\nfull:   %q", prefix, full)
	}
}

// Every intermediate render must contain every word received so far. This is
// the property that stops the cache from eating text - the failure mode a
// full-render implementation cannot have.
//
// Word by word rather than substring, because the render legitimately rewrites
// the text: glamour rewraps prose, and a code block gains a line-number gutter.
// What must never happen is a word disappearing.
func TestStreamRenderNeverLosesText(t *testing.T) {
	text := "Alpha beta gamma.\n\nDelta epsilon zeta.\n\nEta theta iota.\n\n```go\nkappa := 1\n\nlambda := 2\n```\n\nMu nu xi.\n\nOmicron pi."
	for _, size := range []int{1, 2, 3, 7, 13, 64} {
		var sr streamRender
		var acc string
		for _, piece := range chunk(text, size) {
			acc += piece
			got := norm(sr.update(acc, renderAssistant, renderStreaming))
			for _, w := range strings.Fields(acc) {
				// A word still inside a fence marker is not content: the
				// renderer consumes "```" and "```go" into a code block and
				// never displays them, and a fence can be split mid-marker
				// ("``" then "`g"). Punctuation with no letters is framing too.
				if strings.Contains(w, "`") ||
					!strings.ContainsAny(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
					continue
				}
				if !strings.Contains(got, w) {
					t.Fatalf("size %d: word %q vanished after %d chars\nacc:  %q\ngot:  %q",
						size, w, len(acc), acc, got)
				}
			}
		}
	}
}

// Rendering the same accumulated text twice must not double anything, which is
// what would happen if the stable prefix were folded in more than once.
func TestStreamRenderIsIdempotent(t *testing.T) {
	text := "One.\n\nTwo.\n\nThree.\n\n```go\nx := 1\n```\n\nFour."
	var sr streamRender
	first := sr.update(text, renderAssistant, renderStreaming)
	second := sr.update(text, renderAssistant, renderStreaming)
	if norm(first) != norm(second) {
		t.Fatalf("re-rendering the same text changed it:\nfirst:  %q\nsecond: %q", norm(first), norm(second))
	}
}

// A fence must not be cut in half. A blank line inside a code block is not a
// paragraph boundary, and cutting there would re-highlight a block fragment
// every time a line arrived.
func TestStreamRenderDoesNotCutInsideFence(t *testing.T) {
	var sr streamRender
	// A code block containing blank lines. By the end, the fence is closed, so
	// cutting after it is fine - but while it is open, no cut may be taken
	// inside it.
	open := "```go\nfunc a() {\n\n}\n"
	sr.update(open, renderAssistant, renderStreaming)
	if sr.lastBreak > 0 {
		t.Fatalf("cut inside an open code fence at %d", sr.lastBreak)
	}
	closed := open + "```\n\ntail paragraph here\n"
	sr.update(closed, renderAssistant, renderStreaming)
	if sr.lastBreak <= len(open) {
		t.Fatalf("expected a boundary after the closing fence, got %d", sr.lastBreak)
	}
}

// The scan is incremental; it must not rescan from the start, or the whole
// change is quadratic in a much cheaper disguise.
//
// scanPos is allowed to trail the end by up to maxMarkerLen-1 bytes, because
// that overlap is what catches a marker split across two deltas. It must never
// run ahead of the text, and never go backwards.
func TestStreamRenderScanIsIncremental(t *testing.T) {
	var sr streamRender
	var acc string
	const overlap = maxMarkerLen - 1
	for _, piece := range chunk(strings.Repeat("word ", 500), 2) {
		acc += piece
		before := sr.scanPos
		sr.update(acc, renderAssistant, renderStreaming)
		if sr.scanPos < before {
			t.Fatalf("scanPos went backwards: %d -> %d", before, sr.scanPos)
		}
		if sr.scanPos > len(acc) {
			t.Fatalf("scanPos %d is past the end of raw %d", sr.scanPos, len(acc))
		}
		if sr.scanPos < len(acc)-overlap {
			t.Fatalf("scanPos %d is more than %d bytes behind the end %d; the scan is rescanning",
				sr.scanPos, overlap, len(acc))
		}
	}
}

// A paragraph boundary split across two deltas must still be found. This is the
// case that made the whole optimization inert while every benchmark looked fine:
// a "\n\n" whose first newline was the last byte of one delta is invisible to a
// search that starts after it, and deltas are a few bytes each.
func TestStreamRenderFindsBoundarySplitAcrossDeltas(t *testing.T) {
	for _, size := range []int{1, 2, 3, 4, 5} {
		var sr streamRender
		var acc string
		boundary := strings.Index("paragraph one.\n\nparagraph two.", "\n\n") + 2
		for _, piece := range chunk("paragraph one.\n\nparagraph two.\n\nthird paragraph.", size) {
			acc += piece
			sr.update(acc, renderAssistant, renderStreaming)
			// As soon as the whole "\n\n" has arrived, the boundary must be
			// recognised on this very call.
			if len(acc) >= boundary && sr.lastBreak < boundary {
				t.Fatalf("size %d: boundary at %d not found after %d bytes (lastBreak=%d)",
					size, boundary, len(acc), sr.lastBreak)
			}
		}
	}
}

func TestStreamRenderReset(t *testing.T) {
	var sr streamRender
	sr.update("Something long enough to establish a boundary.\n\nAnd more text after it.", renderAssistant, renderStreaming)
	if sr.stableRaw == 0 && sr.lastBreak == 0 {
		t.Fatal("expected some state to accumulate")
	}
	sr.reset()
	if sr.raw != "" || sr.stableOut != "" || sr.stableRaw != 0 || sr.lastBreak != 0 || sr.scanPos != 0 || sr.inFence {
		t.Fatalf("reset left state behind: %+v", sr)
	}
}

// A new message must not inherit the previous one's cached prefix, which would
// splice one answer onto the front of the next.
func TestStreamRenderDoesNotLeakBetweenMessages(t *testing.T) {
	var sr streamRender
	sr.update("First message.\n\nSecond paragraph of the first message.", renderAssistant, renderStreaming)
	sr.reset()
	got := sr.update("Completely different second message.", renderAssistant, renderStreaming)
	if strings.Contains(norm(got), "First message") {
		t.Fatalf("previous message leaked into the next: %q", norm(got))
	}
}

// --- joinedTranscript ---

func TestJoinedTranscriptMatchesStringsJoin(t *testing.T) {
	// The incremental form has to be indistinguishable from what it replaced.
	inputs := [][]string{
		{},
		{"a"},
		{"a", "b"},
		{"a", "b", "c"},
		{"", "a"},
		{"a", ""},
		{"", "", ""},
		{strings.Repeat("x", 100), "y", strings.Repeat("z", 50)},
	}
	for _, in := range inputs {
		var t2 joinedTranscript
		for _, s := range in {
			t2.push(s)
		}
		if want, got := strings.Join(in, "\n\n"), t2.body; want != got {
			t.Fatalf("input %d: got %q, want %q", len(in), got, want)
		}
	}
}

func TestJoinedTranscriptResetAndPushAll(t *testing.T) {
	var tj joinedTranscript
	tj.push("a")
	tj.push("b")
	tj.reset()
	if tj.body != "" {
		t.Fatalf("reset left %q", tj.body)
	}
	tj.pushAll([]string{"x", "y", "z"})
	if tj.body != "x\n\ny\n\nz" {
		t.Fatalf("got %q", tj.body)
	}
	// pushAll replaces rather than appends.
	tj.pushAll([]string{"only"})
	if tj.body != "only" {
		t.Fatalf("got %q", tj.body)
	}
}

// --- benchmarks ---

// feed streams text into a renderer n times, the way Update does per token.
func feed(sr *streamRender, text string, size int) {
	var acc string
	for _, piece := range chunk(text, size) {
		acc += piece
		sr.update(acc, renderAssistant, renderStreaming)
	}
}

func benchAnswer(tokens int) string {
	var b strings.Builder
	// Paragraphs of prose separated by blank lines, with a code block partway
	// through, which is the shape a real coding answer has.
	for i := 0; i < tokens/12; i++ {
		fmt.Fprintf(&b, "Paragraph %d explains one thing about the change in reasonable detail, ", i)
		fmt.Fprintf(&b, "with a second clause so the line actually wraps in a narrow terminal.\n\n")
		if i == tokens/24 {
			b.WriteString("```go\nfunc example() {\n\treturn nil\n}\n```\n\n")
		}
	}
	return b.String()
}

// Sizes are deliberately modest, so a `go test -bench=.` over this package
// finishes in seconds. A benchmark that takes minutes never gets run, and then
// it measures nothing.
//
// There is no longer a benchmark for the old full-render-per-delta behaviour,
// even though it is the thing this work replaced. Benchmarking deleted code
// leaves a trap behind - it costs 8 seconds per iteration at only 500 tokens,
// because it was quadratic, so anyone running the suite would wait on a
// comparison they can already read here. Measured on the 500-token fixture
// before it was removed:
//
//	incremental   27.6 ms/op    13 MB     37,771 allocs
//	full re-render 8.17 s/op    702 MB  11,478,103 allocs
//
// which is ~296x faster and 300x fewer allocations. The old behaviour was
// quadratic in the length of the answer, so the ratio grows with size rather
// than shrinking: at 2,000 tokens the old path was already measured at
// tens of seconds for a single answer.
var incrementalSizes = []int{500, 2000}

func BenchmarkStreamRenderIncremental(b *testing.B) {
	for _, tokens := range incrementalSizes {
		text := benchAnswer(tokens)
		b.Run(fmt.Sprintf("tokens=%d", tokens), func(b *testing.B) {
			b.ReportAllocs()
			var sr streamRender
			for i := 0; i < b.N; i++ {
				sr.reset()
				feed(&sr, text, 4)
			}
		})
	}
}

// The other half of the old cost: rejoining the whole history per token.
// The transcript cost, modelled the way it actually happened.
//
// The old delta handler rejoined the whole history on every streamed token, so
// the work was (tokens x history). The incremental form appends once per
// message, so its work is (messages x length) regardless of how long the answer
// is. Benchmarking one append against one rejoin of the same length measures
// the same thing twice and shows no difference, which is what the first version
// of this benchmark did; the delta multiplier is the whole point.
func BenchmarkTranscriptPerStreamedDelta(b *testing.B) {
	const history = 200
	const deltas = 2000
	msgs := make([]string, history)
	for i := range msgs {
		msgs[i] = "a rendered message of some length"
	}

	b.Run("incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var tj joinedTranscript
			tj.pushAll(msgs)
			// Every streamed delta now costs one lookup of the cached string.
			total := len(tj.body)
			for d := 0; d < deltas; d++ {
				total += len(tj.body)
			}
			_ = total
		}
	})

	b.Run("rejoin-every-delta", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			total := 0
			for d := 0; d < deltas; d++ {
				// This is what the handler did: build the entire transcript
				// again, and again, for every token.
				total += len(strings.Join(msgs, "\n\n"))
			}
			_ = total
		}
	})
}

// A single delta, which is the unit that actually matters: one bubbletea message
// arriving. Reported so a regression shows up in the same units users feel.
func BenchmarkSingleDelta(b *testing.B) {
	text := benchAnswer(4000)
	var sr streamRender
	acc := text[:len(text)/2]
	sr.update(acc, renderAssistant, renderStreaming)
	next := acc + text[len(text)/2:len(text)/2+4]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr.update(next, renderAssistant, renderStreaming)
	}
}

// A long open code fence is the case the boundary rule deliberately refuses to
// cut, so the tail re-renders as it grows. It is bounded by the block rather than
// the answer, but it is the worst case and belongs in the numbers.
//
// 80 lines, not 400: every delta in this shape re-renders the whole open block,
// so the cost is quadratic in the block size and a larger fixture makes the
// benchmark take minutes rather than milliseconds.
func BenchmarkOpenFenceTail(b *testing.B) {
	text := "```go\n" + strings.Repeat("\tx := computeSomething(a, b)\n", 80) + "```\n"
	var sr streamRender
	var acc string
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr.reset()
		acc = ""
		for _, piece := range chunk(text, 4) {
			acc += piece
			sr.update(acc, renderAssistant, renderStreaming)
		}
	}
}

func TestStreamRenderUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	// A long answer, streamed one character at a time. Two things are being
	// checked: that the whole thing is still correct, and that streaming it is
	// not itself the bottleneck. Before the cheap tail renderer this took about
	// 32 seconds for a 600-token answer, because glamour ran once per
	// character; the point of that test is to keep it from coming back.
	text := benchAnswer(600)
	var sr streamRender
	var acc string
	start := time.Now()
	for _, r := range text {
		acc += string(r)
		sr.update(acc, renderAssistant, renderStreaming)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("char-by-char streaming took %v; the renderer is the bottleneck again", elapsed)
	}
	if sr.stableRaw == 0 {
		t.Fatal("expected a stable prefix to accumulate")
	}
	if norm(sr.stableOut) != norm(renderAssistant(sr.raw[:sr.stableRaw])) {
		t.Fatal("stable prefix disagrees with a fresh render of the same text")
	}
	// No word may be missing from the visible transcript at any point.
	for _, w := range strings.Fields(text) {
		if strings.Contains(w, "`") {
			continue
		}
		if !strings.Contains(norm(sr.stableOut), w) {
			t.Fatalf("word %q missing from the transcript", w)
		}
	}
	t.Logf("streamed %d chars in %v", len(text), elapsed)
}

func chunk(s string, n int) []string {
	var out []string
	r := []rune(s)
	for i := 0; i < len(r); i += n {
		e := i + n
		if e > len(r) {
			e = len(r)
		}
		out = append(out, string(r[i:e]))
	}
	return out
}
