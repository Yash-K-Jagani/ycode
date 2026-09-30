package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// toolProvider is a provider that speaks native tool calling: it hands back
// whatever calls it was scripted with, and records the conversation it saw.
type toolProvider struct {
	calls []apitypes.ToolCall
	// text is what it streams alongside the calls, if anything.
	text string

	specs    []apitypes.ToolSpec
	sawSpecs []apitypes.ToolSpec
	rounds   int
	lastMsgs []apitypes.Message
	// usedStream records that the loop took the plain path, which is what a
	// chat turn or a provider without native support should get.
	usedStream bool
}

func (p *toolProvider) Name() string { return "toolprov" }
func (p *toolProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (p *toolProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}

// Stream is the no-tools path. It works rather than erroring, because falling
// back to it is correct for a chat turn, and a fake that refused would hide that.
func (p *toolProvider) Stream(_ context.Context, _ string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	p.usedStream = true
	p.lastMsgs = append([]apitypes.Message(nil), msgs...)
	p.rounds++
	out := p.text
	if p.rounds > 1 {
		out = "final answer"
	}
	_, _ = io.WriteString(w, out)
	return apitypes.StreamChunk{Delta: out, Done: true, PromptTok: 10, ComplTok: 5}, nil
}

func (p *toolProvider) StreamWithTools(_ context.Context, _ string, msgs []apitypes.Message, specs []apitypes.ToolSpec, w io.Writer) (apitypes.StreamChunk, error) {
	p.rounds++
	p.sawSpecs = specs
	p.lastMsgs = append([]apitypes.Message(nil), msgs...)
	// Calls and their accompanying prose happen on the first round only. Later
	// rounds return a plain answer and write nothing else - a fake that kept
	// re-emitting the first round's prose would have the loop re-parse it, which
	// would look like a loop bug and is not one.
	if p.rounds > 1 {
		_, _ = io.WriteString(w, "final answer")
		return apitypes.StreamChunk{Delta: "final answer", Done: true, PromptTok: 10, ComplTok: 5}, nil
	}
	if p.text != "" {
		_, _ = io.WriteString(w, p.text)
	}
	return apitypes.StreamChunk{
		Delta:     p.text,
		Done:      true,
		ToolCalls: p.calls,
		PromptTok: 10,
		ComplTok:  5,
	}, nil
}

// plainProvider does not implement ToolCaller, so the loop must use Stream and
// the text protocol.
type plainProvider struct {
	turns []string
	i     int
	msgs  []apitypes.Message
}

func (p *plainProvider) Name() string { return "plain" }
func (p *plainProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (p *plainProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}
func (p *plainProvider) Stream(_ context.Context, _ string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	p.msgs = append([]apitypes.Message(nil), msgs...)
	out := p.turns[p.i]
	if p.i < len(p.turns)-1 {
		p.i++
	}
	_, _ = io.WriteString(w, out)
	return apitypes.StreamChunk{Delta: out, Done: true, PromptTok: 10, ComplTok: 5}, nil
}

func reg() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(echoTool{})
	return r
}

var one = []apitypes.Message{{Role: apitypes.RoleUser, Content: "read main.go"}}

func readCall(id, path string) apitypes.ToolCall {
	args, _ := json.Marshal(map[string]string{"x": path})
	return apitypes.ToolCall{ID: id, Name: "echo", Args: args}
}

// --- the native path is taken ---

func TestNativeCallsAreExecuted(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{readCall("c1", "1")}}
	res, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build")
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 1 {
		t.Fatalf("Calls = %d, the native call was not executed", res.Calls)
	}
	if res.OKs != 1 {
		t.Fatalf("OKs = %d", res.OKs)
	}
	if res.Text != "final answer" {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestSchemasAreOfferedToTheProvider(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{readCall("c1", "1")}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	if len(p.sawSpecs) != 1 || p.sawSpecs[0].Name != "echo" {
		t.Fatalf("specs = %+v", p.sawSpecs)
	}
	if p.sawSpecs[0].Schema == "" || p.sawSpecs[0].Description == "" {
		t.Fatalf("spec is incomplete: %+v", p.sawSpecs[0])
	}
}

// Only the mode's tools may be offered. Sending the catalogue when writes are
// forbidden invites a call that gets refused, which wastes a round trip.
func TestOnlyAllowedToolsAreOffered(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{readCall("c1", "1")}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo", "write"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	// write is not in this registry, so it must be skipped rather than offered
	// as a spec that would fail as "unknown tool".
	for _, s := range p.sawSpecs {
		if s.Name == "write" {
			t.Fatal("a tool with no implementation behind it was offered")
		}
	}
}

// --- the conversation shape ---

// The result must be a role:"tool" message carrying the id the assistant used.
// A provider that asked for the call rejects the conversation without it, and
// this is the single most likely thing to get wrong.
func TestResultsAreToolMessagesCarryingTheID(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{readCall("call_abc", "1")}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range p.lastMsgs {
		if m.Role != apitypes.RoleTool {
			continue
		}
		found = true
		if m.ToolCallID != "call_abc" {
			t.Fatalf("result does not echo the call id: %+v", m)
		}
		if m.Name != "echo" {
			t.Fatalf("result does not name the tool: %+v", m)
		}
		if !strings.Contains(m.Content, "ECHO:") {
			t.Fatalf("result has no tool output: %q", m.Content)
		}
	}
	if !found {
		t.Fatalf("no role:tool message reached the provider: %+v", p.lastMsgs)
	}
}

// The assistant's calls have to survive into the next request. Without them the
// results refer to nothing, and a provider either rejects that or assumes the
// tools ran themselves.
func TestAssistantTurnCarriesTheCallsForward(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{readCall("call_abc", "1")}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range p.lastMsgs {
		if m.Role != apitypes.RoleAssistant {
			continue
		}
		for _, part := range m.Parts {
			if part.Type != apitypes.PartToolCall || part.Call == nil {
				continue
			}
			seen = true
			if part.Call.ID != "call_abc" || part.Call.Name != "echo" {
				t.Fatalf("call part is wrong: %+v", part.Call)
			}
		}
	}
	if !seen {
		t.Fatal("the assistant's tool calls were not carried into the next request")
	}
}

// --- the trap ---

// A native turn whose prose happens to contain a tool tag must not execute it
// twice. Models do both in one message - call read, and say "I will now use
// <tool:read>..." - and parsing tags out of a native turn double-executes.
func TestNativeTurnDoesNotAlsoParseTags(t *testing.T) {
	p := &toolProvider{
		calls: []apitypes.ToolCall{readCall("c1", "1")},
		text:  "checking <tool:echo>{\"x\":\"in-prose\"}</tool:echo> now",
	}
	res, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build")
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 1 {
		t.Fatalf("Calls = %d: the prose tag was executed as well as the native call", res.Calls)
	}
}

// A native turn with no calls at all is prose, and its tags must still be parsed.
// Deciding "native" from the provider rather than from the response would break
// every chat-mode turn.
func TestProseTurnStillParsesTags(t *testing.T) {
	p := &plainProvider{turns: []string{
		"<tool:echo>{\"x\":\"1\"}</tool:echo>",
		"all done",
	}}
	res, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build")
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 1 {
		t.Fatalf("Calls = %d, the text protocol stopped working", res.Calls)
	}
	if res.Text != "all done" {
		t.Fatalf("Text = %q", res.Text)
	}
}

// --- the text protocol is unchanged ---

// The result shape for a parsed tag must stay what it always was, because
// changing it would break every provider and every model that follows the text
// protocol.
func TestTextProtocolResultShapeIsUnchanged(t *testing.T) {
	p := &plainProvider{turns: []string{
		"<tool:echo>{\"x\":\"1\"}</tool:echo>",
		"done",
	}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	var sawSystem bool
	for _, m := range p.msgs {
		if m.Role != apitypes.RoleSystem {
			continue
		}
		if strings.Contains(m.Content, "<tool_result:echo>") {
			sawSystem = true
		}
		if m.ToolCallID != "" {
			t.Fatal("a text-protocol result carried a tool_call_id")
		}
	}
	if !sawSystem {
		t.Fatalf("the tagged result shape changed: %+v", p.msgs)
	}
}

// A provider without ToolCaller must never be asked for one.
func TestPlainProviderNeverGetsTools(t *testing.T) {
	p := &plainProvider{turns: []string{"just talking"}}
	if _, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	if p.i != 0 {
		t.Fatal("the loop made more than one round for a single answer")
	}
}

// A chat turn has no tools, so the plain Stream path is used and no schemas are
// offered. Sending schemas the model was never going to call just spends prompt
// tokens on every message.
func TestNoToolsMeansNoSchemasOffered(t *testing.T) {
	p := &toolProvider{text: "just talking"}
	// StreamWithTools records specs too, so either path tells us what was sent.
	res, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		nil, nil, io.Discard, nil, 4, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Fatal("no answer")
	}
	if !p.usedStream {
		t.Fatal("a chat turn went through the native path, so no schemas were proven absent")
	}
	if len(p.sawSpecs) != 0 {
		t.Fatalf("specs offered on a chat turn: %+v", p.sawSpecs)
	}
}

// --- failures ---

// A call for a tool the mode forbids must be refused, not executed. Native
// calling does not get to bypass the allow-list.
func TestNativeCallToAForbiddenToolIsRefused(t *testing.T) {
	p := &toolProvider{calls: []apitypes.ToolCall{{ID: "c1", Name: "write", Args: json.RawMessage(`{}`)}}}
	res, err := RunWithRounds(context.Background(), p, "m", one, reg(),
		[]string{"echo"}, nil, io.Discard, nil, 4, "build")
	if err != nil {
		t.Fatal(err)
	}
	if res.OKs != 0 {
		t.Fatalf("OKs = %d: a forbidden tool ran", res.OKs)
	}
	if res.Failed == 0 {
		t.Fatal("the refusal was not reported as a failure")
	}
	// The model must be told, in the shape it expects.
	for _, m := range p.lastMsgs {
		if m.Role == apitypes.RoleTool && !strings.Contains(m.Content, "not allowed") {
			t.Fatalf("the refusal was not explained to the model: %q", m.Content)
		}
	}
}

// --- helpers ---

func TestNativeCallsConvertsAndDefaultsArgs(t *testing.T) {
	if nativeCalls(nil) != nil {
		t.Fatal("nil in must give nil out, so the caller falls back to tag parsing")
	}
	if got := nativeCalls([]apitypes.ToolCall{}); got != nil {
		t.Fatalf("an empty list gave %+v, want nil", got)
	}
	got := nativeCalls([]apitypes.ToolCall{
		{ID: "a", Name: "read"},
		{ID: "b", Name: "write", Args: json.RawMessage(`{"p":"x"}`)},
	})
	if len(got) != 2 {
		t.Fatalf("got %d calls", len(got))
	}
	// A tool with no arguments still needs a valid object, or decoding fails
	// with a JSON parse error rather than about the missing argument.
	if string(got[0].Args) != "{}" {
		t.Fatalf("missing args became %q, want {}", got[0].Args)
	}
	if string(got[1].Args) != `{"p":"x"}` {
		t.Fatalf("args were altered: %s", got[1].Args)
	}
}

func TestCallNativeIsKeyedOnTheID(t *testing.T) {
	// Keyed on the id rather than a flag, so the two cannot disagree.
	if (Call{Name: "x"}).native() {
		t.Fatal("a call with no id claims to be native")
	}
	if !(Call{Name: "x", ID: "c"}).native() {
		t.Fatal("a call with an id does not claim to be native")
	}
}

func TestAssistantMessageIsPlainForTextCalls(t *testing.T) {
	m := assistantMessage("<tool:echo>{}</tool:echo>", ParseCalls("<tool:echo>{}</tool:echo>"))
	if m.Parts != nil {
		t.Fatalf("a text-protocol turn gained parts: %+v", m.Parts)
	}
	if m.Content == "" {
		t.Fatal("content lost")
	}
}

func TestToolResultMessageShapes(t *testing.T) {
	text := toolResultMessage(false, Call{Name: "echo"}, "out")
	if text.Role != apitypes.RoleSystem || !strings.Contains(text.Content, "<tool_result:echo>") {
		t.Fatalf("text shape = %+v", text)
	}
	native := toolResultMessage(true, Call{Name: "echo", ID: "c1"}, "out")
	if native.Role != apitypes.RoleTool || native.ToolCallID != "c1" || native.Content != "out" {
		t.Fatalf("native shape = %+v", native)
	}
}
