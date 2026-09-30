package trace

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/security"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
)

// Explainability traces.
//
// plan.md promised "/debug traces: raw prompts, tool calls, token breakdowns,
// router decisions" from the start. The audit log records that a turn happened;
// it is deliberately coarse, encrypted and append-only. Neither of those is any
// use when the question is "why did the model call read three times and then say
// it was done" - which needs the request, the response, the tool arguments and
// which provider actually answered.
//
// So this is a second, separate thing: a bounded in-memory record of the last
// few turns, for the person at the keyboard, redacted because it is about to be
// printed to their terminal and pasted into an issue.

const (
	// maxEvents bounds how many records are kept.
	//
	// A runaway turn - a model looping on tool calls, or a subagent - would
	// otherwise grow this without limit, and it lives in the same process as an
	// interactive UI. The oldest are dropped, because the question being asked
	// is almost always about what just happened.
	maxEvents = 400

	// maxPayload bounds one record's text.
	//
	// A model can emit a very long response or a tool can return megabytes.
	// Truncation is marked rather than silent, so a truncated trace is not
	// mistaken for a short answer.
	maxPayload = 8 << 10
)

// Kind classifies a record, so a view can group or filter without parsing text.
type Kind string

const (
	KindRequest  Kind = "request"
	KindResponse Kind = "response"
	KindTool     Kind = "tool"
	KindRouter   Kind = "router"
	KindUsage    Kind = "usage"
	KindNote     Kind = "note"
)

// Event is one trace record.
type Event struct {
	Seq int
	At  time.Time
	// Turn groups records belonging to one user turn, so a view can show the
	// last turn rather than an arbitrary slice of everything.
	Turn int
	Kind Kind
	// Provider and Model are set where known. They matter more than they look:
	// a turn that fell back was priced and answered by a different pair from the
	// configured one, and "why is this slow" is usually a provider question.
	Provider string
	Model    string
	// Label is a short human name for the record, e.g. the tool name.
	Label string
	Text  string
	// Truncated marks Text as shortened.
	Truncated bool
	// Err is set when the record describes a failure.
	Err string
}

// Recorder collects events for the current session.
//
// A nil *Recorder is a working no-op, so a caller that has not enabled tracing
// does not need a check at every call site.
type Recorder struct {
	mu     sync.Mutex
	events []Event
	turn   int
	seq    int
}

// New returns a recorder.
func New() *Recorder { return &Recorder{} }

// Reset clears everything, including the turn counter, which is what /debug
// clear is for.
func (r *Recorder) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
	r.turn = 0
	r.seq = 0
}

// NextTurn starts a new turn group and returns its number.
func (r *Recorder) NextTurn() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn++
	return r.turn
}

// add is the single place a record is created, so redaction and bounding cannot
// be forgotten at one of a dozen call sites.
func (r *Recorder) add(e Event) {
	if r == nil {
		return
	}
	e.Text = security.Redact(e.Text)
	e.Err = security.Redact(e.Err)
	if len(e.Text) > maxPayload {
		// TruncateBytes rather than a slice: cutting at a byte boundary lands
		// mid-rune often enough to matter, and a trace full of replacement
		// characters is unreadable exactly when it is needed.
		e.Text = textutil.TruncateBytes(e.Text, maxPayload) + fmt.Sprintf(
			"\n…(truncated, %d bytes total)", len(e.Text))
		e.Truncated = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	if e.Turn == 0 {
		e.Turn = r.turn
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	r.events = append(r.events, e)
	if len(r.events) > maxEvents {
		// Drop the oldest whole records rather than trimming from the front of a
		// single one, so a record is never half-present.
		r.events = append([]Event(nil), r.events[len(r.events)-maxEvents:]...)
	}
}

// Request records what was sent to a provider.
func (r *Recorder) Request(turn int, provider, model string, text string) {
	r.add(Event{Turn: turn, Kind: KindRequest, Provider: provider, Model: model, Text: text})
}

// Response records what came back.
func (r *Recorder) Response(turn int, provider, model, text string) {
	r.add(Event{Turn: turn, Kind: KindResponse, Provider: provider, Model: model, Text: text})
}

// Tool records a tool call and its result.
//
// Args and result are kept separately because the question is usually about the
// pair - a call whose arguments were wrong looks identical to one whose result
// was, and they are fixed in different places.
func (r *Recorder) Tool(turn int, name, args, result string, err error) {
	e := Event{Turn: turn, Kind: KindTool, Label: name, Text: result}
	if args != "" {
		e.Text = "args: " + args + "\nresult: " + result
	}
	if err != nil {
		e.Err = err.Error()
	}
	r.add(e)
}

// Router records a routing decision: which provider was tried, what it cost, and
// why it was rejected.
func (r *Recorder) Router(turn int, text string) {
	r.add(Event{Turn: turn, Kind: KindRouter, Text: text})
}

// Usage records a token breakdown.
func (r *Recorder) Usage(turn int, provider, model string, promptTok, complTok int, usd float64, measured bool) {
	how := "estimated"
	if measured {
		how = "provider-reported"
	}
	r.add(Event{
		Turn: turn, Kind: KindUsage, Provider: provider, Model: model,
		Text: fmt.Sprintf("%d prompt + %d completion tokens, $%.4f (%s)", promptTok, complTok, usd, how),
	})
}

// Note records anything else worth keeping.
func (r *Recorder) Note(turn int, text string) {
	r.add(Event{Turn: turn, Kind: KindNote, Text: text})
}

// Events returns a copy, newest last.
func (r *Recorder) Events() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// Turn returns the records of one turn.
func (r *Recorder) Turn(turn int) []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, e := range r.events {
		if e.Turn == turn {
			out = append(out, e)
		}
	}
	return out
}

// Last returns the most recent turn number, or 0 when nothing was recorded.
func (r *Recorder) Last() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turn
}

// Count is how many records are held.
func (r *Recorder) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// Render formats records for display.
//
// Plain text with no colour, because this gets pasted into a bug report and an
// escape sequence in the middle of a stack trace helps nobody.
func Render(events []Event, full bool) string {
	if len(events) == 0 {
		return "no trace recorded yet — run a turn first"
	}
	var b strings.Builder
	for _, e := range events {
		if !full && e.Kind == KindResponse {
			// A response is usually the thing already on screen. Included under
			// full, excluded by default so a tool-heavy turn is not a wall of
			// text the user has already read.
			continue
		}
		fmt.Fprintf(&b, "#%d t%d %s", e.Seq, e.Turn, e.Kind)
		if e.Provider != "" {
			fmt.Fprintf(&b, " %s", e.Provider)
			if e.Model != "" {
				fmt.Fprintf(&b, "/%s", e.Model)
			}
		}
		if e.Label != "" {
			fmt.Fprintf(&b, " %s", e.Label)
		}
		b.WriteString("\n")
		if e.Err != "" {
			fmt.Fprintf(&b, "  error: %s\n", e.Err)
		}
		for _, line := range strings.Split(e.Text, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
