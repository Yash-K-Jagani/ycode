package tui

import (
	"fmt"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/trace"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// renderTraceMessages renders a request for /debug.
//
// Plain text, one block per message, with tool calls and results shown rather
// than hidden. The reason someone opens a trace is to see what the model was
// actually given, and eliding the parts that "look like noise" is how a debugging
// view becomes useless at exactly the moment it is needed.
//
// A conversation can be long and mostly system prompt. The system message is
// therefore summarised by its first line, with a note of how much was elided -
// it is always the same boilerplate, and 3KB of it hides the two messages that
// matter.
func renderTraceMessages(msgs []apitypes.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n")
		}
		switch m.Role {
		case apitypes.RoleSystem:
			first, rest := splitFirstLine(m.Content)
			if rest > 0 {
				fmt.Fprintf(&b, "system (%d more lines): %s", rest, first)
			} else {
				fmt.Fprintf(&b, "system: %s", first)
			}
		case apitypes.RoleTool:
			// Named and id-linked when present, since a bare role:tool block with
			// no indication of which call it answers is the hardest thing to
			// reason about in a failed turn.
			name := m.Name
			if name == "" {
				name = "(unnamed)"
			}
			if m.ToolCallID != "" {
				fmt.Fprintf(&b, "tool %s (id %s): %s", name, m.ToolCallID, m.Content)
			} else {
				fmt.Fprintf(&b, "tool %s: %s", name, m.Content)
			}
		default:
			fmt.Fprintf(&b, "%s: %s", m.Role, m.Content)
		}
		// Parts carry what Content cannot: tool calls, images, reasoning.
		for _, part := range m.Parts {
			switch part.Type {
			case apitypes.PartToolCall:
				if part.Call == nil {
					continue
				}
				id := part.Call.ID
				if id == "" {
					id = "(no id)"
				}
				fmt.Fprintf(&b, "\n  -> calls %s (id %s) %s", part.Call.Name, id, string(part.Call.Args))
			case apitypes.PartToolResult:
				fmt.Fprintf(&b, "\n  -> result: %s", part.Result)
			case apitypes.PartImage:
				fmt.Fprintf(&b, "\n  -> image (%s, %d bytes)", part.Mime, len(part.Data))
			case apitypes.PartReasoning:
				fmt.Fprintf(&b, "\n  -> reasoning: %s", part.Text)
			}
		}
	}
	return b.String()
}

func splitFirstLine(s string) (string, int) {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i], strings.Count(s, "\n")
	}
	return s, 0
}

// debugView renders the trace for the /debug command.
//
// turn <= 0 means the most recent turn, which is what someone opening /debug
// almost always wants; the others are for going back through a session.
func (m *Model) debugView(turn int, full bool) string {
	if m.tracer == nil {
		return "tracing is not available"
	}
	if turn <= 0 {
		turn = m.tracer.Last()
	}
	if turn <= 0 {
		return "no turn recorded yet — run one, then /debug"
	}
	events := m.tracer.Turn(turn)
	if len(events) == 0 {
		return fmt.Sprintf("turn %d has no trace records (kept: %d across %d turns)",
			turn, m.tracer.Count(), m.tracer.Last())
	}
	head := fmt.Sprintf("trace for turn %d — %d record(s), %d kept across %d turn(s)",
		turn, len(events), m.tracer.Count(), m.tracer.Last())
	if !full {
		head += "\n(responses hidden; /debug full to include them)"
	}
	// Said every time, because this output is meant to be pasted into an issue
	// and redaction is pattern matching rather than a guarantee. A person who
	// knows that will read it before pasting; one who does not will not.
	head += "\n(secrets matching known formats are redacted; anything else is not — check before sharing)"
	return head + "\n\n" + trace.Render(events, full)
}
