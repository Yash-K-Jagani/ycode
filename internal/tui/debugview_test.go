package tui

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/trace"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func tracedModel() *Model {
	return &Model{tracer: trace.New()}
}

// --- the command ---

func TestDebugSaysSoBeforeAnyTurn(t *testing.T) {
	m := tracedModel()
	got := m.debugView(0, false)
	if !strings.Contains(got, "no turn recorded") {
		t.Fatalf("got %q", got)
	}
}

func TestDebugShowsTheLastTurnByDefault(t *testing.T) {
	m := tracedModel()
	a := m.tracer.NextTurn()
	m.tracer.Request(a, "gemini", "m", "first request")
	b := m.tracer.NextTurn()
	m.tracer.Request(b, "gemini", "m", "second request")

	got := m.debugView(0, false)
	if !strings.Contains(got, "second request") {
		t.Fatalf("the latest turn is missing: %q", got)
	}
	if strings.Contains(got, "first request") {
		t.Fatalf("an older turn leaked into the default view: %q", got)
	}
	if !strings.Contains(got, "turn 2") {
		t.Fatalf("the turn number is not stated: %q", got)
	}
}

func TestDebugCanGoBackToAnEarlierTurn(t *testing.T) {
	m := tracedModel()
	a := m.tracer.NextTurn()
	m.tracer.Request(a, "gemini", "m", "first request")
	m.tracer.NextTurn()

	got := m.debugView(a, false)
	if !strings.Contains(got, "first request") {
		t.Fatalf("got %q", got)
	}
}

func TestDebugReportsAnEmptyTurn(t *testing.T) {
	m := tracedModel()
	m.tracer.NextTurn()
	got := m.debugView(1, false)
	if !strings.Contains(got, "no trace records") {
		t.Fatalf("got %q", got)
	}
}

// This output is pasted into issues, so the limit of redaction has to be stated
// rather than assumed by the reader.
func TestDebugWarnsAboutRedaction(t *testing.T) {
	m := tracedModel()
	n := m.tracer.NextTurn()
	m.tracer.Request(n, "gemini", "m", "hello")
	if !strings.Contains(m.debugView(0, false), "check before sharing") {
		t.Fatal("no warning about unrecognised secrets")
	}
}

func TestDebugFullIncludesResponses(t *testing.T) {
	m := tracedModel()
	n := m.tracer.NextTurn()
	m.tracer.Request(n, "gemini", "m", "the prompt")
	m.tracer.Response(n, "gemini", "m", "the answer")
	if strings.Contains(m.debugView(0, false), "the answer") {
		t.Fatal("responses are in the default view")
	}
	if !strings.Contains(m.debugView(0, true), "the answer") {
		t.Fatal("full view is missing the response")
	}
}

// A Model built without a tracer must not panic, since /debug is available from
// a hand-built Model in tests and from any future surface that skips New.
func TestDebugWithoutATracer(t *testing.T) {
	m := &Model{}
	if got := m.debugView(0, false); !strings.Contains(got, "not available") {
		t.Fatalf("got %q", got)
	}
}

// --- request rendering ---

// The reason someone opens a trace is to see what the model was actually given,
// so the parts that carry structure have to be shown.
func TestRequestRenderingShowsToolCalls(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "you are helpful\nline two\nline three"},
		{Role: apitypes.RoleUser, Content: "read main.go"},
		{Role: apitypes.RoleAssistant, Parts: []apitypes.Part{{
			Type: apitypes.PartToolCall,
			Call: &apitypes.ToolCall{ID: "call_1", Name: "read", Args: []byte(`{"path":"main.go"}`)},
		}}},
		{Role: apitypes.RoleTool, Content: "package main", ToolCallID: "call_1", Name: "read"},
	}
	out := renderTraceMessages(msgs)

	for _, want := range []string{"read main.go", "call_1", `"path":"main.go"`, "package main"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The system prompt is always the same boilerplate and can be kilobytes.
	// It is summarised, with the elision stated rather than silent.
	if !strings.Contains(out, "you are helpful") {
		t.Error("the system prompt head is missing")
	}
	if !strings.Contains(out, "2 more lines") {
		t.Errorf("elided lines are not accounted for:\n%s", out)
	}
}

// A role:tool block with no indication of which call it answers is the hardest
// thing to reason about in a failed turn.
func TestToolResultSaysWhichCallItAnswers(t *testing.T) {
	out := renderTraceMessages([]apitypes.Message{
		{Role: apitypes.RoleTool, Content: "out", ToolCallID: "call_9", Name: "grep"},
	})
	if !strings.Contains(out, "call_9") || !strings.Contains(out, "grep") {
		t.Fatalf("got %q", out)
	}
	// Ollama has no ids, so the unnamed case has to be legible too.
	bare := renderTraceMessages([]apitypes.Message{{Role: apitypes.RoleTool, Content: "out"}})
	if !strings.Contains(bare, "unnamed") {
		t.Fatalf("an idless tool result is not marked: %q", bare)
	}
}

func TestRequestRenderingCoversEveryPartType(t *testing.T) {
	out := renderTraceMessages([]apitypes.Message{{
		Role: apitypes.RoleUser,
		Parts: []apitypes.Part{
			{Type: apitypes.PartImage, Data: []byte{1, 2, 3}, Mime: "image/png"},
			{Type: apitypes.PartReasoning, Text: "considering options"},
			{Type: apitypes.PartToolResult, Result: "tool output"},
		},
	}})
	for _, want := range []string{"image/png", "3 bytes", "considering options", "tool output"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A call with no id is the Ollama case and must not render as a broken one.
func TestCallWithNoIDIsMarkedNotBroken(t *testing.T) {
	out := renderTraceMessages([]apitypes.Message{{
		Role:  apitypes.RoleAssistant,
		Parts: []apitypes.Part{{Type: apitypes.PartToolCall, Call: &apitypes.ToolCall{Name: "todo"}}},
	}})
	if !strings.Contains(out, "no id") {
		t.Fatalf("got %q", out)
	}
	if strings.Contains(out, `<nil>`) {
		t.Fatalf("a nil call rendered as %q", out)
	}
}

func TestNilCallPartDoesNotPanic(t *testing.T) {
	out := renderTraceMessages([]apitypes.Message{{
		Role:  apitypes.RoleAssistant,
		Parts: []apitypes.Part{{Type: apitypes.PartToolCall}},
	}})
	if strings.Contains(out, "<nil>") {
		t.Fatalf("got %q", out)
	}
}
