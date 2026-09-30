package openaicompat

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

var oneMsg = []apitypes.Message{{Role: apitypes.RoleUser, Content: "read main.go"}}

var readSpec = apitypes.ToolSpec{
	Name:        "read",
	Description: "read a file",
	Schema:      `{"type":"object","properties":{"path":{"type":"string"}}}`,
}

// sseServer replies with the given raw SSE body and records the request.
func sseServer(t *testing.T, body string) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func stream(t *testing.T, specs []apitypes.ToolSpec, body string) (apitypes.StreamChunk, []byte) {
	t.Helper()
	srv, got := sseServer(t, body)
	c := New("testprov", srv.URL, "sk-test")
	chunk, err := c.StreamWithTools(context.Background(), "m", oneMsg, specs, io.Discard)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return chunk, *got
}

// --- reassembly ---

// The wire format splits one call across three chunks. Treating each as a whole
// call yields three calls with truncated JSON; this is the test that catches it.
func TestToolCallIsReassembledAcrossChunks(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"read","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"main.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":""}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(chunk.ToolCalls), chunk.ToolCalls)
	}
	call := chunk.ToolCalls[0]
	if call.Name != "read" {
		t.Fatalf("name = %q", call.Name)
	}
	if call.ID != "call_a" {
		t.Fatalf("id = %q", call.ID)
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("args did not reassemble into valid JSON: %v (%q)", err, call.Args)
	}
	if args.Path != "main.go" {
		t.Fatalf("path = %q, the argument fragments were not concatenated", args.Path)
	}
}

// A model that calls two tools in one message must produce two calls, in order.
func TestParallelToolCallsKeepTheirOrder(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"read","arguments":"{\"path\":\"b.go\"}"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 2 {
		t.Fatalf("got %d calls: %+v", len(chunk.ToolCalls), chunk.ToolCalls)
	}
	if chunk.ToolCalls[0].ID != "a" || chunk.ToolCalls[1].ID != "b" {
		t.Fatalf("order not preserved: %+v", chunk.ToolCalls)
	}
}

// Interleaved fragments are the case a single shared buffer gets wrong: the
// arguments of two calls land in each other.
func TestInterleavedFragmentsDoNotCross(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"read","arguments":"{\"pa"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"grep","arguments":"{\"pa"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"th\":\"b\"}"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 2 {
		t.Fatalf("got %d calls: %+v", len(chunk.ToolCalls), chunk.ToolCalls)
	}
	if !strings.Contains(string(chunk.ToolCalls[0].Args), "a.go") ||
		strings.Contains(string(chunk.ToolCalls[0].Args), "b") {
		t.Fatalf("first call absorbed the second's fragments: %q", chunk.ToolCalls[0].Args)
	}
	if !strings.Contains(string(chunk.ToolCalls[1].Args), `"b"`) {
		t.Fatalf("second call is wrong: %q", chunk.ToolCalls[1].Args)
	}
}

// Several servers omit the index when there is only ever one call. Rejecting
// that would break the majority of requests over an optional field.
func TestMissingIndexIsTreatedAsOneCall(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"id":"c","function":{"name":"read","arguments":"{\"pa"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"th\":\"main.go\"}"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(chunk.ToolCalls), chunk.ToolCalls)
	}
	if !strings.Contains(string(chunk.ToolCalls[0].Args), "main.go") {
		t.Fatalf("args = %q", chunk.ToolCalls[0].Args)
	}
}

// A fragment with no function name is not a tool call. Passing it on would give
// the harness a call to the empty tool and a confusing "unknown tool" error
// instead of the real problem.
func TestNamelessFragmentIsDropped(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 0 {
		t.Fatalf("a nameless fragment became a call: %+v", chunk.ToolCalls)
	}
}

// Malformed JSON is dropped rather than handed on. Executing it would fail at a
// decode error naming an internal type, instead of showing the model's own text.
func TestMalformedArgumentsAreDropped(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"read","arguments":"{\"path\":"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 0 {
		t.Fatalf("invalid JSON became a call: %+v", chunk.ToolCalls)
	}
}

// A tool call with no arguments at all is legal - many tools take none.
func TestCallWithNoArgumentsSurvives(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"todo"}}]}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 1 || chunk.ToolCalls[0].Name != "todo" {
		t.Fatalf("a no-argument call was lost: %+v", chunk.ToolCalls)
	}
}

// --- prose stays prose ---

// Nil means prose. An empty non-nil slice would mean "the model asked for
// something we could not read", and callers need to tell those apart.
func TestNoToolCallsMeansProse(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"hello"}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if chunk.ToolCalls != nil {
		t.Fatalf("ToolCalls = %+v, want nil for a prose answer", chunk.ToolCalls)
	}
	if chunk.Delta != "hello" {
		t.Fatalf("delta = %q", chunk.Delta)
	}
}

// A model may call a tool and then explain itself in the same turn. Both must
// survive, or the explanation is lost.
func TextAndToolCallsBothArrive(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"let me look"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"read","arguments":"{}"}}]}}]}

data: {"choices":[{"delta":{"content":" — done"}}]}

data: [DONE]

`
	chunk, _ := stream(t, []apitypes.ToolSpec{readSpec}, body)
	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("calls = %+v", chunk.ToolCalls)
	}
	if !strings.Contains(chunk.Delta, "let me look") || !strings.Contains(chunk.Delta, "done") {
		t.Fatalf("text around the call was lost: %q", chunk.Delta)
	}
}

// --- the request ---

// The schema has to reach the provider, or the model is still guessing.
func TestSchemasReachTheRequest(t *testing.T) {
	_, got := stream(t, []apitypes.ToolSpec{readSpec}, "data: [DONE]\n\n")
	var req map[string]any
	if err := json.Unmarshal(got, &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("no tools in the request: %v", req["tools"])
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "read" {
		t.Fatalf("name = %v", fn["name"])
	}
	if fn["description"] != "read a file" {
		t.Fatalf("description = %v", fn["description"])
	}
	if !strings.Contains(fn["parameters"].(string), "path") {
		t.Fatalf("schema not sent: %v", fn["parameters"])
	}
	if req["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", req["tool_choice"])
	}
}

// An empty list must produce no "tools" key at all. Several servers reject an
// empty array, which would mean supporting tool calls broke every request from
// a user with no tools enabled.
func TestNoSpecsMeansNoToolsKey(t *testing.T) {
	_, got := stream(t, nil, "data: [DONE]\n\n")
	var req map[string]any
	if err := json.Unmarshal(got, &req); err != nil {
		t.Fatal(err)
	}
	if _, present := req["tools"]; present {
		t.Fatalf("an empty tools key was sent: %v", req["tools"])
	}
}

// tool_choice auto is the default for OpenAI-compatible servers; forcing "none"
// or "required" would break the many turns that are just conversation.
func TestToolChoiceIsAutoNotRequired(t *testing.T) {
	p := toolParams([]apitypes.ToolSpec{readSpec})
	if p["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", p["tool_choice"])
	}
	if toolParams(nil) != nil {
		t.Fatal("toolParams(nil) should add nothing")
	}
}

// --- interface conformance ---

// The optional interface is discovered by type assertion, so a provider that
// does not implement it must not accidentally satisfy it.
func TestToolCallerConformance(t *testing.T) {
	// Asserted against a local shape rather than providers.ToolCaller: that
	// package imports this one, so importing it back from a test is a cycle.
	type toolCaller interface {
		StreamWithTools(context.Context, string, []apitypes.Message, []apitypes.ToolSpec, io.Writer) (apitypes.StreamChunk, error)
	}
	var p any = New("x", "http://127.0.0.1:1", "k")
	if _, ok := p.(toolCaller); !ok {
		t.Fatal("openaicompat does not satisfy the ToolCaller shape")
	}
	// The plain Stream path must still work, with no tools offered.
	if _, err := New("x", "http://127.0.0.1:1", "").Stream(context.Background(), "m", oneMsg, io.Discard); err == nil {
		t.Fatal("a missing API key should error")
	}
}

// --- errors ---

// A provider error must still surface. Tool support added a new body field and
// a new response shape, either of which could hide a 400.
func TestProviderErrorStillSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"tools unsupported"}}`)
	}))
	t.Cleanup(srv.Close)
	_, err := New("testprov", srv.URL, "sk-test").
		StreamWithTools(context.Background(), "m", oneMsg, []apitypes.ToolSpec{readSpec}, io.Discard)
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	if !strings.Contains(err.Error(), "tools unsupported") {
		t.Fatalf("the provider's reason was lost: %v", err)
	}
}

// A non-stream body is still detected as not-an-event-stream.
func TestNonStreamResponseStillDetected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>login</body></html>")
	}))
	t.Cleanup(srv.Close)
	_, err := New("testprov", srv.URL, "sk-test").
		StreamWithTools(context.Background(), "m", oneMsg, []apitypes.ToolSpec{readSpec}, io.Discard)
	if err == nil {
		t.Fatal("an HTML login page came back as a successful empty answer")
	}
	if !strings.Contains(err.Error(), "event stream") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// --- the builder in isolation ---

func TestToolBuilderRejectsNothingItShouldAccept(t *testing.T) {
	b := newToolBuilder()
	b.add(0, "a", "read", `{"path":"x"}`)
	b.add(1, "b", "grep", `{"q":"y"}`)
	got := b.calls()
	if len(got) != 2 || got[0].Name != "read" || got[1].Name != "grep" {
		t.Fatalf("calls = %+v", got)
	}
	// A repeated name overwrites rather than concatenates. A server that sends
	// the name on every fragment sends the same value, so either behaviour works
	// for it - but one that appends produces "readread" and an "unknown tool"
	// error for a tool the model named correctly.
	b2 := newToolBuilder()
	b2.add(0, "a", "read", "")
	b2.add(0, "", "read", "{}")
	c := b2.calls()
	if len(c) != 1 || c[0].Name != "read" {
		t.Fatalf("name handling wrong: %+v", c)
	}
	// An empty builder must report prose, not an empty list.
	if newToolBuilder().calls() != nil {
		t.Fatal("an empty builder returned a non-nil slice")
	}
}
