package apitypes

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTextOnlyWireFormatIsUnchanged is the load-bearing test for this package.
//
// []Message is marshalled straight onto provider request bodies by
// internal/providers/openaicompat, and read by the HTTP API and the Go SDK. If
// adding Parts changed the bytes a text-only message produces, every existing
// provider, every saved session and every SDK consumer would break at once.
// So the exact JSON is pinned rather than merely round-tripped.
func TestTextOnlyWireFormatIsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{"user", Message{Role: RoleUser, Content: "hello"}, `{"role":"user","content":"hello"}`},
		{"system", Message{Role: RoleSystem, Content: "be brief"}, `{"role":"system","content":"be brief"}`},
		{"assistant", Message{Role: RoleAssistant, Content: "done"}, `{"role":"assistant","content":"done"}`},
		{"empty content", Message{Role: RoleUser}, `{"role":"user","content":""}`},
		{"unicode", Message{Role: RoleUser, Content: "héllo → 世界"}, `{"role":"user","content":"héllo → 世界"}`},
		{"empty parts is omitted", Message{Role: RoleUser, Content: "x", Parts: []Part{}},
			`{"role":"user","content":"x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("wire format changed\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}

// A message with no text at all must still serialise, because an image-only
// turn is a legitimate user message.
func TestEmptyMessageMarshals(t *testing.T) {
	got, err := json.Marshal(Message{Role: RoleUser})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != `{"role":"user","content":""}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnmarshalLegacyShape(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"legacy text"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != RoleUser || m.Content != "legacy text" {
		t.Fatalf("got %+v", m)
	}
	if len(m.Parts) != 0 {
		t.Fatalf("a legacy message should carry no parts, got %+v", m.Parts)
	}
}

func TestUnmarshalTextOnlyPartsCollapse(t *testing.T) {
	// A text-only parts array is redundant with content and must collapse to
	// nil, so a session file does not bloat with a second copy of every turn.
	var m Message
	err := json.Unmarshal([]byte(`{"role":"user","content":"hi","parts":[{"type":"text","text":"hi"}]}`), &m)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Parts) != 0 {
		t.Fatalf("text-only parts should collapse to nil, got %+v", m.Parts)
	}
	if m.Content != "hi" {
		t.Fatalf("content should survive, got %q", m.Content)
	}
}

func TestUnmarshalStructuredRoundTrip(t *testing.T) {
	orig := Message{Role: RoleUser, Content: "look at this"}.WithImage([]byte{0x89, 'P', 'N', 'G'}, "image/png")
	if orig.Content != "look at this" {
		t.Fatalf("adding an image must not drop the text, got %q", orig.Content)
	}
	if !orig.HasStructuredParts() {
		t.Fatal("expected structured parts")
	}
	blob, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Content != orig.Content {
		t.Fatalf("content lost: %q vs %q", back.Content, orig.Content)
	}
	if len(back.Parts) != 2 {
		t.Fatalf("expected text+image parts, got %+v", back.Parts)
	}
	if back.Parts[1].Type != PartImage || back.Parts[1].Mime != "image/png" {
		t.Fatalf("image part did not survive: %+v", back.Parts[1])
	}
	if string(back.Parts[1].Data) != string([]byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("image bytes did not survive: %v", back.Parts[1].Data)
	}
}

func TestWithImageDoesNotMutateReceiver(t *testing.T) {
	// These run in loops over a shared base message, so an in-place append
	// that reused the backing array would leak one turn's image into the next.
	base := Message{Role: RoleUser, Content: "same"}
	a := base.WithImage([]byte("A"), "image/png")
	b := base.WithImage([]byte("B"), "image/png")
	if string(a.Parts[1].Data) != "A" {
		t.Fatalf("first image aliased: %q", a.Parts[1].Data)
	}
	if string(b.Parts[1].Data) != "B" {
		t.Fatalf("second image aliased: %q", b.Parts[1].Data)
	}
	if len(base.Parts) != 0 {
		t.Fatalf("receiver was mutated: %+v", base.Parts)
	}
	if base.Content != "same" {
		t.Fatalf("receiver content changed: %q", base.Content)
	}
}

func TestImageIsBase64OnTheWire(t *testing.T) {
	// Every vision API expects base64 in a content part, which is what
	// encoding/json does for []byte. Pin it so a field type change is caught.
	m := Message{Role: RoleUser}.WithImage([]byte{0, 1, 2}, "image/png")
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"data":"AAEC"`) {
		t.Fatalf("image data is not base64: %s", blob)
	}
}

func TestToolCallsPreserveOrder(t *testing.T) {
	m := Message{Role: RoleAssistant}
	m = m.WithToolCall(ToolCall{ID: "1", Name: "read"})
	m = m.WithToolCall(ToolCall{ID: "2", Name: "grep"})
	m = m.WithToolCall(ToolCall{ID: "3", Name: "edit"})
	calls := m.ToolCalls()
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	want := []string{"read", "grep", "edit"}
	for i, c := range calls {
		if c.Name != want[i] {
			t.Fatalf("call %d = %q, want %q", i, c.Name, want[i])
		}
	}
}

func TestWithToolCallFoldsExistingText(t *testing.T) {
	// A model that emits prose and a tool call in one turn must not lose the
	// prose, because Content is what the transcript shows and what the
	// tolerant text parser reads.
	m := Message{Role: RoleAssistant, Content: "let me look"}.WithToolCall(ToolCall{Name: "read"})
	if !strings.Contains(m.Content, "let me look") {
		t.Fatalf("text lost: %q", m.Content)
	}
	if len(m.Parts) != 2 {
		t.Fatalf("want text+tool_call, got %+v", m.Parts)
	}
}

func TestToolResultStaysOutOfContent(t *testing.T) {
	// A tool result is not user-facing prose. If it leaked into Content it
	// would be re-rendered into the chat pane by the TUI's formatMsg.
	m := Message{Role: RoleTool}.WithToolResult("1", "file contents here", false)
	if strings.Contains(m.Content, "file contents here") {
		t.Fatalf("tool result leaked into content: %q", m.Content)
	}
	if len(m.Parts) != 1 || m.Parts[0].Type != PartToolResult {
		t.Fatalf("expected a tool_result part, got %+v", m.Parts)
	}
	if m.Parts[0].IsError {
		t.Fatal("IsError should be false")
	}
}

func TestToolResultErrorSurvivesRoundTrip(t *testing.T) {
	m := Message{Role: RoleTool}.WithToolResult("7", "traceback: nil map", true)
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Parts) != 1 || !back.Parts[0].IsError {
		t.Fatalf("error flag lost: %+v", back.Parts)
	}
	if back.Parts[0].Result != "traceback: nil map" {
		t.Fatalf("partial output lost: %q", back.Parts[0].Result)
	}
	if back.Parts[0].Call.ID != "7" {
		t.Fatalf("call id lost: %+v", back.Parts[0].Call)
	}
}

func TestTextOfPartsIgnoresNonText(t *testing.T) {
	parts := []Part{
		{Type: PartText, Text: "a"},
		{Type: PartImage, Data: []byte("x")},
		{Type: PartReasoning, Text: "b"},
		{Type: PartToolCall, Call: &ToolCall{Name: "read"}},
	}
	if got := TextOfParts(parts); got != "ab" {
		t.Fatalf("got %q, want %q", got, "ab")
	}
}

func TestTextMsgHelpers(t *testing.T) {
	if m := UserMsg("x"); m.Role != RoleUser || m.Content != "x" || m.Parts != nil {
		t.Fatalf("UserMsg: %+v", m)
	}
	if m := SystemMsg("x"); m.Role != RoleSystem {
		t.Fatalf("SystemMsg: %+v", m)
	}
	if m := AssistantMsg("x"); m.Role != RoleAssistant {
		t.Fatalf("AssistantMsg: %+v", m)
	}
}

func TestUsageReported(t *testing.T) {
	if (StreamChunk{PromptTok: 10}).UsageReported() != true {
		t.Fatal("prompt tokens should count as reported")
	}
	if (StreamChunk{ComplTok: 5}).UsageReported() != true {
		t.Fatal("completion tokens should count as reported")
	}
	// The whole point: a provider that reports nothing must not look like it
	// reported zero tokens, which is what an estimate would otherwise show.
	if (StreamChunk{Delta: "hi"}).UsageReported() != false {
		t.Fatal("no tokens means not reported")
	}
}

func TestVisionHeuristic(t *testing.T) {
	yes := []string{"llava:7b", "qwen2.5-vl:7b", "llama3.2-vision:11b", "moondream", "gemma3:12b", "minicpm-v"}
	no := []string{"qwen2.5-coder:3b", "llama3:8b", "deepseek-coder:6.7b", "gemma2:9b", ""}
	for _, id := range yes {
		if !(ModelInfo{ID: id}).Vision() {
			t.Errorf("%q should be detected as vision-capable", id)
		}
	}
	for _, id := range no {
		if (ModelInfo{ID: id}).Vision() {
			t.Errorf("%q should not be detected as vision-capable", id)
		}
	}
}

// A session is a slice of these; make sure the slice form round-trips too, not
// just a single message, because that is the shape actually stored.
//
// The three built values are assigned to variables first: inside a composite
// literal the element type may be elided, so a `{...}.Method()` element stops
// parsing.
func TestSessionRoundTrip(t *testing.T) {
	withCall := Message{Role: RoleAssistant, Content: "here"}.WithToolCall(ToolCall{ID: "t1", Name: "read"})
	withResult := Message{Role: RoleTool}.WithToolResult("t1", "package main", false)
	withShot := UserMsg("and this screenshot").WithImage([]byte{0xFF, 0xD8}, "image/jpeg")

	in := []Message{
		SystemMsg("rules"),
		UserMsg("read a.go"),
		withCall,
		withResult,
		AssistantMsg("it declares func main"),
		withShot,
	}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out []Message
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d messages, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Role != in[i].Role {
			t.Errorf("msg %d role = %q, want %q", i, out[i].Role, in[i].Role)
		}
		if out[i].Content != in[i].Content {
			t.Errorf("msg %d content = %q, want %q", i, out[i].Content, in[i].Content)
		}
		if len(out[i].Parts) != len(in[i].Parts) {
			t.Errorf("msg %d parts = %+v, want %+v", i, out[i].Parts, in[i].Parts)
		}
	}
	shot := out[5]
	// The text was folded into a part when the image was attached, so this is
	// text+image, not image alone.
	if len(shot.Parts) != 2 || shot.Parts[1].Type != PartImage {
		t.Errorf("image part lost in session round trip: %+v", shot.Parts)
	}
	if string(shot.Parts[1].Data) != string([]byte{0xFF, 0xD8}) {
		t.Errorf("image bytes lost: %v", shot.Parts[1].Data)
	}
}

// The existing session store writes Content and nothing else. Those files must
// keep loading after this change, which is the same as the legacy test but
// through the array path, matching what is on disk today.
func TestExistingSessionFilesStillLoad(t *testing.T) {
	onDisk := `[{"role":"system","content":"you are ycode"},{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`
	var out []Message
	if err := json.Unmarshal([]byte(onDisk), &out); err != nil {
		t.Fatalf("existing session files must keep loading: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d, want 3", len(out))
	}
	for i, m := range out {
		if m.HasStructuredParts() {
			t.Errorf("msg %d unexpectedly has parts: %+v", i, m.Parts)
		}
	}
}
