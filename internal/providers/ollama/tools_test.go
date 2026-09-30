package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// ndjson serves Ollama's stream shape: bare JSON objects one per line, no "data:"
// prefix. A test written against the OpenAI dialect would pass here and read
// nothing, which is why this exists as its own fixture.
func ndjson(t *testing.T, lines ...string) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, l := range lines {
			_, _ = io.WriteString(w, l+"\n")
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

var chat = []apitypes.Message{{Role: apitypes.RoleUser, Content: "read main.go"}}

var readSpec = apitypes.ToolSpec{
	Name:        "read",
	Description: "read a file",
	Schema:      `{"type":"object","properties":{"path":{"type":"string"}}}`,
}

func stream(t *testing.T, specs []apitypes.ToolSpec, lines ...string) (apitypes.StreamChunk, []byte) {
	t.Helper()
	srv, got := ndjson(t, lines...)
	chunk, err := New(srv.URL).StreamWithTools(context.Background(), "qwen", chat, specs, io.Discard)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return chunk, *got
}

// --- the dialect differences ---

// arguments is an object in Ollama's dialect and a string in OpenAI's. Reading
// it as a string yields nothing, so the call silently loses its arguments and
// the tool then fails on a missing path.
func TestArgumentsArriveAsAnObject(t *testing.T) {
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec},
		`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read","arguments":{"path":"main.go"}}}]}}`,
		`{"message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":10,"eval_count":5}`,
	)
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("got %d calls: %+v", len(chunk.ToolCalls), chunk.ToolCalls)
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(chunk.ToolCalls[0].Args, &args); err != nil {
		t.Fatalf("arguments did not decode: %v (%s)", err, chunk.ToolCalls[0].Args)
	}
	if args.Path != "main.go" {
		t.Fatalf("path = %q", args.Path)
	}
}

// Ollama reports real token counts, and they must survive the delegation to
// StreamWithTools.
func TestUsageSurvives(t *testing.T) {
	chunk, _ := stream(t, nil,
		`{"message":{"content":"hi"},"done":true,"prompt_eval_count":123,"eval_count":45}`,
	)
	if !chunk.UsageReported() {
		t.Fatal("usage was lost")
	}
	if chunk.PromptTok != 123 || chunk.ComplTok != 45 {
		t.Fatalf("usage = %d + %d", chunk.PromptTok, chunk.ComplTok)
	}
}

func TestProseTurnHasNoCalls(t *testing.T) {
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec},
		`{"message":{"content":"hello"},"done":true}`,
	)
	// Nil, so the loop falls back to parsing tags. An empty slice would read as
	// "asked for something we could not read".
	if chunk.ToolCalls != nil {
		t.Fatalf("ToolCalls = %+v, want nil", chunk.ToolCalls)
	}
	if chunk.Delta != "hello" {
		t.Fatalf("delta = %q", chunk.Delta)
	}
}

// Text alongside a call must survive: a model may call a tool and explain.
func TestTextAndCallsBothArrive(t *testing.T) {
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec},
		`{"message":{"content":"let me look"},"done":false}`,
		`{"message":{"content":"","tool_calls":[{"function":{"name":"read","arguments":{"path":"a.go"}}}]},"done":false}`,
		`{"message":{"content":"done"},"done":true}`,
	)
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("calls = %+v", chunk.ToolCalls)
	}
	if !strings.Contains(chunk.Delta, "let me look") || !strings.Contains(chunk.Delta, "done") {
		t.Fatalf("text around the call was lost: %q", chunk.Delta)
	}
}

func TestParallelCallsKeepOrder(t *testing.T) {
	// One object per line: that is what NDJSON is, and a multi-line object here
	// would be split by the scanner into unparseable fragments.
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec},
		`{"message":{"tool_calls":[{"function":{"name":"read","arguments":{"path":"a.go"}}},{"function":{"name":"grep","arguments":{"q":"b"}}}]},"done":true}`,
	)
	if len(chunk.ToolCalls) != 2 {
		t.Fatalf("calls = %+v", chunk.ToolCalls)
	}
	if chunk.ToolCalls[0].Name != "read" || chunk.ToolCalls[1].Name != "grep" {
		t.Fatalf("order not preserved: %+v", chunk.ToolCalls)
	}
}

// --- the request ---

func TestSchemasReachTheRequest(t *testing.T) {
	_, got := stream(t, []apitypes.ToolSpec{readSpec}, `{"message":{"content":"x"},"done":true}`)
	var req map[string]any
	if err := json.Unmarshal(got, &req); err != nil {
		t.Fatalf("request is not JSON: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("no tools: %v", req["tools"])
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "read" {
		t.Fatalf("name = %v", fn["name"])
	}
	if !strings.Contains(fn["parameters"].(string), "path") {
		t.Fatalf("schema not sent: %v", fn["parameters"])
	}
}

// An empty list must send no "tools" key. Some Ollama versions reject an empty
// array outright.
func TestNoSpecsMeansNoToolsKey(t *testing.T) {
	_, got := stream(t, nil, `{"message":{"content":"x"},"done":true}`)
	var req map[string]any
	if err := json.Unmarshal(got, &req); err != nil {
		t.Fatal(err)
	}
	if _, present := req["tools"]; present {
		t.Fatalf("an empty tools key was sent: %v", req["tools"])
	}
}

// --- the outgoing translation ---

// The most likely thing to get wrong: our internal parts array is not Ollama's
// tool_calls, so the assistant's calls must be translated or the results that
// follow refer to nothing.
func TestAssistantToolCallsAreTranslated(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleUser, Content: "read main.go"},
		{Role: apitypes.RoleAssistant, Parts: []apitypes.Part{{
			Type: apitypes.PartToolCall,
			Call: &apitypes.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"main.go"}`)},
		}}},
		{Role: apitypes.RoleTool, Content: "file contents", ToolCallID: "c1", Name: "read"},
	}
	w := toWire(msgs)

	if w[0].Role != "user" || w[0].Content != "read main.go" {
		t.Fatalf("user message altered: %+v", w[0])
	}
	asst := w[1]
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("the assistant's call was not translated: %+v", asst)
	}
	if asst.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("name = %q", asst.ToolCalls[0].Function.Name)
	}
	// An object, not a string. Marshalling the raw Args would produce a string
	// and Ollama would reject the turn.
	var args map[string]string
	if err := json.Unmarshal(asst.ToolCalls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("arguments did not decode as an object: %v (%s)", err, asst.ToolCalls[0].Function.Arguments)
	}
	if args["path"] != "main.go" {
		t.Fatalf("arguments = %v", args)
	}
	// A plain assistant message must not carry a tool_calls key at all.
	if asst.ToolCalls == nil {
		t.Fatal("expected one call")
	}

	// The result is role:"tool" with the text as content. Ollama's protocol has
	// no id to echo, and it ignores the extras.
	res := w[2]
	if res.Role != "tool" {
		t.Fatalf("result role = %q, want tool", res.Role)
	}
	if res.Content != "file contents" {
		t.Fatalf("result content = %q", res.Content)
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("a result message carried tool calls: %+v", res)
	}
}

// A plain conversation must translate to plain messages, with no empty keys
// that some versions treat as malformed.
func TestPlainMessagesStayPlain(t *testing.T) {
	w := toWire([]apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "you are helpful"},
		{Role: apitypes.RoleAssistant, Content: "hi"},
	})
	if len(w) != 2 {
		t.Fatalf("got %d messages", len(w))
	}
	for _, m := range w {
		if m.ToolCalls != nil {
			t.Fatalf("a plain message grew tool_calls: %+v", m)
		}
		if m.Images != nil {
			t.Fatalf("a plain message grew images: %+v", m)
		}
	}
}

// A tool call with no arguments still needs a valid object.
func TestCallWithNoArgumentsGetsAnObject(t *testing.T) {
	w := toWire([]apitypes.Message{{
		Role:  apitypes.RoleAssistant,
		Parts: []apitypes.Part{{Type: apitypes.PartToolCall, Call: &apitypes.ToolCall{Name: "todo"}}},
	}})
	if string(w[0].ToolCalls[0].Function.Arguments) != "{}" {
		t.Fatalf("args = %s, want {}", w[0].ToolCalls[0].Function.Arguments)
	}
}

// A tool result carried as a part, which is how the SDK expresses one, has to
// become text for Ollama.
func TestToolResultPartBecomesText(t *testing.T) {
	w := toWire([]apitypes.Message{{
		Role:  apitypes.RoleTool,
		Parts: []apitypes.Part{{Type: apitypes.PartToolResult, Result: "42"}},
	}})
	if w[0].Content != "42" {
		t.Fatalf("content = %q, want the result text", w[0].Content)
	}
}

// This also fixes image input for Ollama, which did not work before: parts were
// marshalled as "parts" and Ollama ignored the field entirely.
func TestImagePartsBecomeOllamaImages(t *testing.T) {
	w := toWire([]apitypes.Message{{
		Role: apitypes.RoleUser,
		Parts: []apitypes.Part{
			{Type: apitypes.PartText, Text: "what is this"},
			{Type: apitypes.PartImage, Data: []byte{0x89, 'P', 'N', 'G'}, Mime: "image/png"},
		},
	}})
	if len(w[0].Images) != 1 {
		t.Fatalf("images = %v", w[0].Images)
	}
	// Ollama takes base64 with no data: prefix, which is what encoding to a
	// string from raw bytes gives.
	if w[0].Images[0] != "iVBORw==" {
		t.Fatalf("image = %q, want base64 with no prefix", w[0].Images[0])
	}
}

// A part with no data must not become an empty image, which some versions
// reject as malformed.
func TestEmptyImagePartIsSkipped(t *testing.T) {
	w := toWire([]apitypes.Message{{
		Role:  apitypes.RoleUser,
		Parts: []apitypes.Part{{Type: apitypes.PartImage}},
	}})
	if w[0].Images != nil {
		t.Fatalf("an empty image part became %v", w[0].Images)
	}
}

// --- the collector ---

// Ollama normally sends a whole call, but a model that streams one in two pieces
// would otherwise produce two half-calls.
func TestCollectorMergesByPosition(t *testing.T) {
	c := newCallCollector()
	c.add(0, apitypes.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"a"}`)})
	c.add(1, apitypes.ToolCall{Name: "grep", Args: json.RawMessage(`{"q":"b"}`)})
	got := c.calls()
	if len(got) != 2 || got[0].Name != "read" || got[1].Name != "grep" {
		t.Fatalf("calls = %+v", got)
	}
	if newCallCollector().calls() != nil {
		t.Fatal("an empty collector returned a non-nil slice")
	}
}

// A call with no name is not a call; passing it on produces a call to the empty
// tool and an "unknown tool" error instead of the real problem.
func TestCollectorDropsNamelessCalls(t *testing.T) {
	c := newCallCollector()
	c.add(0, apitypes.ToolCall{Args: json.RawMessage(`{}`)})
	if c.calls() != nil {
		t.Fatalf("a nameless call survived: %+v", c.calls())
	}
}

func TestCollectorDefaultsEmptyArgs(t *testing.T) {
	c := newCallCollector()
	c.add(0, apitypes.ToolCall{Name: "todo"})
	if got := c.calls(); len(got) != 1 || string(got[0].Args) != "{}" {
		t.Fatalf("args = %q, want {}", got[0].Args)
	}
}

// --- errors ---

// A daemon error must still surface. The request body changed shape, which is
// exactly the kind of change that can hide a 400.
func TestDaemonErrorStillSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"tools not supported by this model"}`)
	}))
	t.Cleanup(srv.Close)
	_, err := New(srv.URL).StreamWithTools(context.Background(), "qwen", chat,
		[]apitypes.ToolSpec{readSpec}, io.Discard)
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("the daemon's reason was lost: %v", err)
	}
}

// A line that will not parse used to be skipped silently, which turned an
// unreadable response into a successful empty answer.
func TestUnreadableResponseIsAnError(t *testing.T) {
	chunk, err := func() (apitypes.StreamChunk, error) {
		srv, _ := ndjson(t, "not json at all", "still not json")
		return New(srv.URL).StreamWithTools(context.Background(), "qwen", chat, nil, io.Discard)
	}()
	if err == nil {
		t.Fatalf("an unreadable response succeeded with %+v", chunk)
	}
	if !strings.Contains(err.Error(), "could not read") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// --- interface shape ---

// Asserted against a local shape: the providers package imports this one.
func TestToolCallerShape(t *testing.T) {
	type toolCaller interface {
		StreamWithTools(context.Context, string, []apitypes.Message, []apitypes.ToolSpec, io.Writer) (apitypes.StreamChunk, error)
	}
	var p any = New("http://127.0.0.1:1")
	if _, ok := p.(toolCaller); !ok {
		t.Fatal("ollama does not satisfy the ToolCaller shape")
	}
}
