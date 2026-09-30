// Package apitypes holds the request/response types shared by the HTTP API,
// the Go SDK, the providers and the session store.
//
// # Why Message has both Content and Parts
//
// Message originally had a single Content string, which cannot express
// anything a model can consume beyond text: an image, a tool call, a tool
// result, a reasoning block. Rather than replace the field and rewrite all 40
// construction sites plus 34 read sites, Parts is additive and Content stays
// authoritative for text.
//
// The invariant is deliberately narrow, and everything else follows from it:
//
//   - A message whose Parts is nil or contains only text parts is exactly what
//     it was before Parts existed. Content is the whole truth.
//   - A message whose Parts contains anything Content cannot express (an image,
//     a tool call, a tool result) keeps Content as the concatenation of its
//     text parts, so every existing .Content reader keeps working, and Parts as
//     the full-fidelity record.
//
// This matters because []Message is marshalled straight onto provider request
// bodies (see internal/providers/openaicompat.Stream), so a text-only message
// must serialise byte-identically to how it always has. That is why Parts is
// omitted entirely unless the caller set it.
package apitypes

import (
	"encoding/json"
	"strings"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type PartType string

const (
	// PartText is ordinary prose. A message made only of these needs no Parts
	// at all; Content carries it.
	PartText PartType = "text"
	// PartImage is an inline image (screenshot, diagram) for vision models.
	PartImage PartType = "image"
	// PartToolCall is a model request to run a tool. The agent loop uses
	// these when a provider supports native tool calling.
	PartToolCall PartType = "tool_call"
	// PartToolResult is the outcome of a tool call, correlated by ToolCallID.
	PartToolResult PartType = "tool_result"
	// PartReasoning is visible model reasoning (thinking mode). Providers that
	// do not emit it simply never produce one.
	PartReasoning PartType = "reasoning"
)

// ToolCall is one model request to invoke a tool.
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Part is one piece of a Message.
//
// Only Text is used for text-only messages, and even then only when the caller
// built Parts explicitly. Data is base64-encoded on the wire by encoding/json
// for the []byte type, which is what every vision API expects.
type Part struct {
	Type PartType `json:"type"`
	Text string   `json:"text,omitempty"`
	// Data holds image bytes. Kept as []byte so the standard encoder emits
	// base64, matching the OpenAI/Gemini content-part shape.
	Data []byte `json:"data,omitempty"`
	Mime string `json:"mime,omitempty"`
	// Call is set for PartToolCall. Result is the tool's text for
	// PartToolResult, and IsError distinguishes an empty successful result
	// from a failure. A failed tool usually still returns output the model
	// needs, so the distinction has to survive a round trip through the
	// session store rather than being flattened into "no result".
	Call    *ToolCall `json:"call,omitempty"`
	Result  string    `json:"result,omitempty"`
	IsError bool      `json:"is_error,omitempty"`
}

// Message is one conversation turn.
//
// Content is the flattened text and remains the field callers should reach for
// unless they specifically need to know about images or tool calls, in which
// case they should call HasStructuredParts first.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// Parts is populated only when the message carries something Content
	// cannot express. Text-only messages leave it nil.
	Parts []Part `json:"parts,omitempty"`
}

// messageWire is the on-the-wire shape. It is the historical one exactly:
// {role, content}, plus an optional parts array. Older readers that do not
// know about parts still decode a text-only message correctly, and a rich
// message degrades to its text rather than becoming unparseable.
type messageWire struct {
	Role  Role   `json:"role"`
	Text  string `json:"content"`
	Parts []Part `json:"parts,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(messageWire{Role: m.Role, Text: m.Content, Parts: m.Parts})
}

// UnmarshalJSON accepts both shapes and re-establishes the invariant: whatever
// arrives, Content ends up holding the flattened text, and Parts is nil unless
// the message genuinely needs it.
//
// A session written before Parts existed has only content. A session written
// by a later version may have parts. Both must load, and neither may lose its
// text.
func (m *Message) UnmarshalJSON(b []byte) error {
	var w messageWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.Parts = normalizeParts(w.Parts)
	m.Content = w.Text
	if m.Content == "" && len(m.Parts) > 0 {
		m.Content = TextOfParts(m.Parts)
	}
	return nil
}

// normalizeParts collapses a text-only parts list to nil, because a message
// whose parts are all text is indistinguishable from one with no parts, and
// storing the redundant form would bloat every session file for no gain.
func normalizeParts(p []Part) []Part {
	structured := false
	for i := range p {
		if p[i].Type != PartText {
			structured = true
			break
		}
	}
	if !structured {
		return nil
	}
	return p
}

// HasStructuredParts reports whether the message carries anything its Content
// string cannot express. Callers on the hot path that only handle text can use
// this to skip the Parts walk entirely.
func (m Message) HasStructuredParts() bool { return len(m.Parts) > 0 }

// TextMsg builds a plain text message. It is the constructor every call site
// that means "just words" should use, so that Parts is provably nil there.
func TextMsg(role Role, text string) Message { return Message{Role: role, Content: text} }

// UserMsg, SystemMsg and AssistantMsg are role-specific shorthands.
func UserMsg(text string) Message      { return TextMsg(RoleUser, text) }
func SystemMsg(text string) Message    { return TextMsg(RoleSystem, text) }
func AssistantMsg(text string) Message { return TextMsg(RoleAssistant, text) }

// TextOfParts concatenates the text of every text and reasoning part. Image,
// tool_call and tool_result parts contribute nothing, because they have no
// textual form that a prompt can carry.
func TextOfParts(parts []Part) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case PartText, PartReasoning:
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

// WithImage returns m with an image part appended, and Content refreshed to the
// concatenation of its text parts. An existing plain-text message therefore
// gains an image without losing its text, and a message that already has parts
// gains one without disturbing the others.
//
// The returned message is a copy; the receiver is not modified, because these
// are built inside loops and aliasing a shared slice would be a subtle bug.
func (m Message) WithImage(data []byte, mime string) Message {
	parts := make([]Part, 0, len(m.Parts)+1)
	parts = append(parts, m.Parts...)
	if m.Content != "" && len(m.Parts) == 0 {
		// Fold the existing text into a part so Content stays derivable.
		parts = append(parts, Part{Type: PartText, Text: m.Content})
	}
	parts = append(parts, Part{Type: PartImage, Data: data, Mime: mime})
	m.Parts = parts
	m.Content = TextOfParts(parts)
	return m
}

// WithToolCall returns m carrying a tool call request.
func (m Message) WithToolCall(call ToolCall) Message {
	parts := m.partsWithText()
	parts = append(parts, Part{Type: PartToolCall, Call: &call})
	m.Parts = parts
	m.Content = TextOfParts(parts)
	return m
}

// WithToolResult returns m carrying the outcome of the tool call id. The
// result text is deliberately not folded into Content: a tool result is not
// prose the user should see re-rendered in the chat pane, and the agent loop
// already emits its own <tool_result:> framing for the text protocol.
func (m Message) WithToolResult(id, text string, isErr bool) Message {
	parts := m.partsWithText()
	parts = append(parts, Part{
		Type:    PartToolResult,
		Call:    &ToolCall{ID: id},
		Result:  text,
		IsError: isErr,
	})
	m.Parts = parts
	m.Content = TextOfParts(parts)
	return m
}

// ToolCalls returns the tool calls this message requests, in order. An empty
// slice means the message is prose.
func (m Message) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, p := range m.Parts {
		if p.Type == PartToolCall && p.Call != nil {
			out = append(out, *p.Call)
		}
	}
	return out
}

// partsWithText returns m's parts with any plain Content folded in, so a part
// append never orphans the text that was set via the Content field.
func (m Message) partsWithText() []Part {
	if len(m.Parts) > 0 {
		return append(make([]Part, 0, len(m.Parts)+1), m.Parts...)
	}
	parts := make([]Part, 0, 1)
	if m.Content != "" {
		parts = append(parts, Part{Type: PartText, Text: m.Content})
	}
	return parts
}

type StreamChunk struct {
	Delta string `json:"delta"`
	Done  bool   `json:"done"`
	// PromptTok and ComplTok are provider-reported counts. They are zero when
	// the provider does not report usage, in which case callers must fall back
	// to an estimate and say so rather than presenting the estimate as fact.
	PromptTok int `json:"prompt_tokens,omitempty"`
	ComplTok  int `json:"completion_tokens,omitempty"`

	// ToolCalls are the calls the model asked for natively, rather than by
	// writing a <tool:name>{...}</tool:name> tag into its answer.
	//
	// Nil means the model produced prose. An empty non-nil slice would mean it
	// produced a tool call we could not read, which is a different thing and is
	// why these are appended to rather than assigned.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolSpec describes one tool to a provider that supports native tool calling.
//
// It is the wire form, not the execution form: the agent's Tool interface stays
// exactly as it is, and this is the projection of it a model needs. Keeping the
// two apart is what lets a tool gain streaming or subagent delegation without
// anything a provider has to understand changing.
type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Schema is a JSON Schema object as a string. A string rather than
	// json.RawMessage because several providers require the schema nested under
	// "function", and letting each one re-marshal it invites subtle differences
	// in what actually goes on the wire.
	Schema string `json:"parameters,omitempty"`
}

// UsageReported reports whether the provider actually sent counts, which is the
// difference between a measured number and a guess.
func (c StreamChunk) UsageReported() bool { return c.PromptTok > 0 || c.ComplTok > 0 }

type ModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Size     string `json:"size,omitempty"`
	Quant    string `json:"quant,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Vision reports whether the model is one that can consume image parts.
// Deliberately a name heuristic, like the context-window table: no provider
// API can be asked this cheaply, and getting it wrong degrades to a clear
// error rather than a wrong answer.
func (m ModelInfo) Vision() bool {
	id := strings.ToLower(m.ID)
	for _, needle := range []string{
		"llava", "bakllava", "vision", "-vl", "vl-", "qwen2-vl", "qwen2.5-vl",
		"minicpm-v", "moondream", "gemma3", "pixtral", "llama3.2-vision",
		"internvl", "cogvlm", "phi-3.5-vision", "phi4-multimodal",
	} {
		if strings.Contains(id, needle) {
			return true
		}
	}
	return false
}
