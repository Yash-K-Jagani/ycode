package trace

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// The recorder lives in the same process as an interactive UI, so the bounds are
// not a nicety.
func TestEventsAreBounded(t *testing.T) {
	r := New()
	for i := 0; i < maxEvents*3; i++ {
		r.Note(0, "x")
	}
	if got := r.Count(); got != maxEvents {
		t.Fatalf("kept %d events, cap is %d", got, maxEvents)
	}
}

// The newest records are the ones wanted, so the oldest go.
func TestTheNewestAreKept(t *testing.T) {
	r := New()
	for i := 0; i < maxEvents+10; i++ {
		r.Note(0, "n")
	}
	got := r.Events()
	if len(got) != maxEvents {
		t.Fatalf("kept %d", len(got))
	}
	if got[len(got)-1].Seq != maxEvents+10 {
		t.Fatalf("last seq = %d, want the newest", got[len(got)-1].Seq)
	}
	if got[0].Seq != 11 {
		t.Fatalf("first kept seq = %d, want 11 (the oldest ten dropped)", got[0].Seq)
	}
}

// Truncation is marked, so a shortened trace is not mistaken for a short answer.
func TestLongPayloadsAreTruncatedAndMarked(t *testing.T) {
	r := New()
	r.Note(0, strings.Repeat("x", maxPayload*2))
	e := r.Events()[0]
	if !e.Truncated {
		t.Fatal("a truncated payload is not marked")
	}
	if len(e.Text) > maxPayload+64 {
		t.Fatalf("kept %d bytes, cap is %d", len(e.Text), maxPayload)
	}
	if !strings.Contains(e.Text, "truncated") {
		t.Fatalf("truncation is not stated: %q", e.Text[len(e.Text)-60:])
	}
}

// A byte-bounded cut lands mid-rune often enough to matter, and a trace full of
// replacement characters is unreadable exactly when it is needed.
func TestTruncationDoesNotSplitARune(t *testing.T) {
	r := New()
	// Three-byte runes, so a naive byte cut lands inside one.
	r.Note(0, strings.Repeat("日", maxPayload))
	e := r.Events()[0]
	if !utf8.ValidString(e.Text) {
		t.Fatalf("truncation produced invalid UTF-8: %q", e.Text[maxPayload-8:maxPayload+8])
	}
	if strings.Contains(e.Text, "�") {
		t.Fatal("truncation produced a replacement character")
	}
}

// This gets pasted into an issue, so a key in a prompt or a tool argument must
// not survive into the text.
func TestSecretsAreRedacted(t *testing.T) {
	r := New()
	r.Request(1, "gemini", "m", "here is my key sk-ant-api03-SECRETVALUE1234567890 please")
	e := r.Events()[0]
	if strings.Contains(e.Text, "SECRETVALUE1234567890") {
		t.Fatalf("a secret survived redaction: %q", e.Text)
	}
	if !strings.Contains(e.Text, "REDACTED") {
		t.Fatalf("nothing marks the redaction: %q", e.Text)
	}
}

// Errors go through redaction too; a provider error can echo a request header.
func TestErrorTextIsRedacted(t *testing.T) {
	r := New()
	r.Tool(1, "bash", `{"command":"export GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"}`, "",
		errStr("rejected: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"))
	e := r.Events()[0]
	if strings.Contains(e.Text+e.Err, "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345") {
		t.Fatalf("a secret survived: text=%q err=%q", e.Text, e.Err)
	}
}

// Redaction is pattern matching, not a guarantee.
//
// The rules cover real key formats - AKIA, ghp_, npm_, AIza, sk-, xox, JWT,
// and any key/token/secret/password assignment of sufficient length. A secret
// that matches none of those is not caught. This test documents the limit rather
// than pretending it does not exist, and /debug says so to the user, because
// the output gets pasted into issues.
func TestRedactionIsPatternMatchingNotAGuarantee(t *testing.T) {
	r := New()
	// Not a recognised format and not an assignment of a recognised name.
	r.Note(1, "handwritten note: portal token is q7f2m9x")
	out := r.Events()[0].Text
	if strings.Contains(out, "[REDACTED]") {
		t.Fatalf("unexpectedly redacted: %q", out)
	}
	if !strings.Contains(out, "q7f2m9x") {
		t.Fatal("the test is not exercising what it claims")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

// Turns group records so /debug can show the last turn rather than an arbitrary
// slice of everything.
func TestRecordsAreGroupedByTurn(t *testing.T) {
	r := New()
	a := r.NextTurn()
	r.Request(a, "gemini", "m", "first")
	r.Response(a, "gemini", "m", "answer")
	b := r.NextTurn()
	r.Request(b, "gemini", "m", "second")

	if len(r.Turn(a)) != 2 {
		t.Fatalf("turn %d has %d records", a, len(r.Turn(a)))
	}
	if len(r.Turn(b)) != 1 {
		t.Fatalf("turn %d has %d records", b, len(r.Turn(b)))
	}
	if r.Last() != b {
		t.Fatalf("Last = %d, want %d", r.Last(), b)
	}
}

// A record added without an explicit turn joins the current one, which is what
// the router relies on since it does not know when a turn starts.
func TestRecordsDefaultToTheCurrentTurn(t *testing.T) {
	r := New()
	t1 := r.NextTurn()
	r.Note(0, "inside turn 1")
	r.NextTurn()
	r.Note(0, "inside turn 2")
	if got := r.Turn(t1); len(got) != 1 || got[0].Text != "inside turn 1" {
		t.Fatalf("turn %d = %+v", t1, got)
	}
}

// A nil recorder is a working no-op, so a caller that has not enabled tracing
// needs no check at every call site.
func TestNilRecorderIsSafe(t *testing.T) {
	var r *Recorder
	r.Reset()
	if n := r.NextTurn(); n != 0 {
		t.Fatalf("NextTurn on nil = %d", n)
	}
	r.Request(1, "p", "m", "text")
	r.Response(1, "p", "m", "text")
	r.Tool(1, "bash", "{}", "out", nil)
	r.Router(1, "text")
	r.Usage(1, "p", "m", 1, 2, 0.1, true)
	r.Note(1, "text")
	if r.Count() != 0 || r.Last() != 0 || len(r.Events()) != 0 || len(r.Turn(1)) != 0 {
		t.Fatal("a nil recorder reported state")
	}
}

// Concurrent use: the turn goroutine records while the UI thread renders.
func TestConcurrentRecording(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Note(0, "concurrent")
				_ = r.Count()
				_ = r.Events()
			}
		}()
	}
	wg.Wait()
	if r.Count() == 0 {
		t.Fatal("nothing was recorded")
	}
	if r.Count() > maxEvents {
		t.Fatalf("the cap was breached under concurrency: %d", r.Count())
	}
}

// --- rendering ---

func TestRenderSaysSoWhenEmpty(t *testing.T) {
	if got := Render(nil, false); !strings.Contains(got, "no trace") {
		t.Fatalf("empty render = %q", got)
	}
}

func TestRenderOmitsResponsesByDefault(t *testing.T) {
	r := New()
	n := r.NextTurn()
	r.Request(n, "gemini", "m", "the prompt")
	r.Response(n, "gemini", "m", "the answer")
	r.Tool(n, "read", `{"path":"a.go"}`, "file body", nil)

	short := Render(r.Events(), false)
	if !strings.Contains(short, "the prompt") {
		t.Fatalf("the request is missing: %q", short)
	}
	// A response is usually already on screen, so it is opt-in.
	if strings.Contains(short, "the answer") {
		t.Fatalf("responses are not meant to be in the default view: %q", short)
	}
	full := Render(r.Events(), true)
	if !strings.Contains(full, "the answer") {
		t.Fatalf("full view is missing the response: %q", full)
	}
}

func TestRenderNamesTheProviderAndErrors(t *testing.T) {
	r := New()
	n := r.NextTurn()
	r.Tool(n, "bash", "", "", errStr("exit status 1"))
	out := Render(r.Events(), false)
	if !strings.Contains(out, "bash") {
		t.Fatalf("the tool is unnamed: %q", out)
	}
	if !strings.Contains(out, "exit status 1") {
		t.Fatalf("the error is missing: %q", out)
	}
	if !strings.Contains(out, "gemini") && !strings.Contains(out, "tool") {
		t.Fatalf("nothing identifies the record: %q", out)
	}
}

// No colour of its own: this gets pasted into a bug report, and an escape
// sequence in the middle of a stack trace helps nobody.
//
// Content is passed through faithfully, including an escape sequence that was
// already there - stripping it would corrupt the trace it exists to show.
func TestRenderAddsNoEscapesOfItsOwn(t *testing.T) {
	r := New()
	r.Note(r.NextTurn(), "ordinary text")
	r.Request(r.Last(), "gemini", "m", "a prompt")
	r.Usage(r.Last(), "gemini", "m", 1, 2, 0.001, true)
	for _, e := range []string{"short", "full"} {
		if out := Render(r.Events(), e == "full"); strings.Contains(out, "\x1b") {
			t.Fatalf("render (%s) emitted an escape sequence: %q", e, out)
		}
	}
}
