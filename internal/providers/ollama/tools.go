package ollama

import (
	"encoding/base64"
	"encoding/json"

	"github.com/Yash-K-Jagani/ycode/internal/providers/toolwire"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Ollama's wire format for tool calling.
//
// It is close to OpenAI's and differs in three ways that each break a direct
// reuse of apitypes.Message:
//
//   - Tool calls live in message.tool_calls, not in a content parts array.
//   - arguments is a JSON object, not the string OpenAI sends.
//   - There are no call ids at all, so a result cannot be correlated by id.
//
// That last one is why the loop tracks provenance separately from the id: an
// Ollama call is native and has no id, and a rule derived from the id would
// classify it as text-protocol and send back a message shape Ollama does not
// understand.

// wireMessage is one message as Ollama expects it.
type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls is omitted entirely for a plain message. Ollama treats an empty
	// array as a malformed turn on some versions, the same trap as sending an
	// empty "tools" array in the request.
	ToolCalls []wireCall `json:"tool_calls,omitempty"`
	// Images is base64, one entry per image part, which is Ollama's shape rather
	// than the nested object every vision API uses.
	//
	// This also fixes image input for Ollama, which did not work before: parts
	// were marshalled as "parts" and Ollama ignored the field.
	Images []string `json:"images,omitempty"`
}

type wireCall struct {
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name string `json:"name"`
	// Arguments is an object here. Marshalling apitypes.ToolCall.Args directly
	// would produce a string, which Ollama rejects with a decode error naming an
	// internal type instead of anything about arguments.
	Arguments json.RawMessage `json:"arguments"`
}

// toWire translates the internal message list into Ollama's shape.
//
// Written out rather than reusing the OpenAI-compatible path because the two
// dialects disagree in a way that matters, and a translation that tries to serve
// both produces messages neither accepts.
func toWire(msgs []apitypes.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		w := wireMessage{Role: string(m.Role), Content: m.Content}
		for _, part := range m.Parts {
			switch part.Type {
			case apitypes.PartToolCall:
				if part.Call == nil {
					continue
				}
				args := part.Call.Args
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				w.ToolCalls = append(w.ToolCalls, wireCall{
					Function: wireFunction{Name: part.Call.Name, Arguments: args},
				})
			case apitypes.PartImage:
				if len(part.Data) == 0 {
					continue
				}
				w.Images = append(w.Images, base64.StdEncoding.EncodeToString(part.Data))
			}
		}
		// A tool result whose content is carried as a part rather than in
		// Content, which is how the SDK expresses one. Ollama wants it as text.
		for _, part := range m.Parts {
			if part.Type == apitypes.PartToolResult {
				w.Content = part.Result
			}
		}
		out = append(out, w)
	}
	return out
}

// toolParams is the request extension.
//
// Identical in shape to the OpenAI-compatible one - both follow the function
// calling convention - but built here rather than shared, so neither dialect's
// quirks leak into the other.
func toolParams(specs []apitypes.ToolSpec) map[string]any {
	if len(specs) == 0 {
		return nil
	}
	// Cached: the spec list is identical between turns, and rebuilding these
	// maps was the largest constant on the wire for a request.
	return map[string]any{"tools": toolwire.List(specs)}
}

// callCollector gathers tool calls across a stream.
//
// Ollama sends a complete call in one message rather than fragments, so this is
// much simpler than the OpenAI-compatible builder. It still handles a call
// arriving split, because "typically" is not "always" and a model that streams a
// call in two pieces would otherwise produce two half-calls.
type callCollector struct {
	byIndex map[int]apitypes.ToolCall
	order   []int
}

func newCallCollector() *callCollector {
	return &callCollector{byIndex: map[int]apitypes.ToolCall{}}
}

func (c *callCollector) add(index int, call apitypes.ToolCall) {
	if _, seen := c.byIndex[index]; !seen {
		c.order = append(c.order, index)
	}
	c.byIndex[index] = call
}

// calls returns the collected calls, or nil when there were none. Nil means
// "prose", which is what lets the loop fall back to the text protocol.
func (c *callCollector) calls() []apitypes.ToolCall {
	if len(c.order) == 0 {
		return nil
	}
	out := make([]apitypes.ToolCall, 0, len(c.order))
	for _, i := range c.order {
		call := c.byIndex[i]
		// A call with no name is not a call; passing it on produces a call to
		// the empty tool and an "unknown tool" error instead of the real problem.
		if call.Name == "" {
			continue
		}
		if len(call.Args) == 0 {
			call.Args = json.RawMessage("{}")
		}
		out = append(out, call)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
