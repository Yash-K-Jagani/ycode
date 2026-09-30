package tui

import (
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Live tool output.
//
// A long-running tool used to show nothing at all while it ran. On a TUI that
// is indistinguishable from a hang, and the reasonable response to a hang is to
// cancel work that was nearly finished - so the user either waits through what
// looks like a freeze or destroys a four-minute test run to find out what was
// happening.
//
// What is shown here is deliberately provisional. It lives outside the
// transcript, is replaced wholesale on every chunk, and is dropped when the call
// finishes because the complete output arrives then as a permanent card.

const (
	// maxLiveToolBytes caps what is retained for the live view.
	//
	// Rendering is not free: every chunk re-renders the block and re-syncs the
	// viewport, so an unbounded buffer turns a chatty build into a frozen UI.
	// This is the same lesson as the context trim's O(n^2) rewrite, learned the
	// expensive way, applied cheaply the second time.
	maxLiveToolBytes = 8 * 1024

	// maxLiveToolLines caps how many lines of it are drawn. A build log is mostly
	// scrolling noise and the last lines are the ones being waited for.
	maxLiveToolLines = 12

	// maxToolOutBytes caps the permanent output card.
	//
	// The full output is not lost - it is already capped by the tool itself, and
	// the model still receives everything within the tool's own cap - this only
	// stops the transcript growing without bound.
	maxToolOutBytes = 4000
)

// liveToolStyle is dim so that in-flight output reads as provisional and does
// not compete with the assistant's answer for attention.
func liveToolStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7a7a7a")).
		Italic(true)
}

// livePreview renders the tool currently streaming, or "" when nothing is.
//
// Returned as a tail argument to syncViewport rather than appended, so it is not
// part of the permanent history.
func (m *Model) livePreview() string {
	if m.liveTool == "" || len(m.liveBuf) == 0 {
		return ""
	}
	return renderLiveTool(m.liveTool, string(m.liveBuf))
}

func renderLiveTool(tool, out string) string {
	return liveToolStyle().Render("⋯ "+tool+" running") + "\n" +
		liveToolStyle().Render(tailLines(out, maxLiveToolLines))
}

// clearLiveTool drops the provisional block. Called when a tool finishes.
func (m *Model) clearLiveTool() {
	m.liveTool = ""
	m.liveBuf = m.liveBuf[:0]
}

// tailLines keeps the last n lines of s, dropping the rest.
//
// The head is dropped rather than the middle because the end is where a build or
// a test run explains itself: the failing test name, the compile error, the
// summary line. Leading "Compiling foo v0.1.0" lines are the part nobody reads.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return "…\n" + strings.Join(lines[len(lines)-n:], "\n")
}

// streamedOutput returns the tool output worth putting in the transcript, and
// streamedOutput returns the tool output worth putting in the transcript, and
// "" for tools that do not stream.
//
// The guard lives here rather than at the call site so that adding a second
// caller cannot forget it and dump a whole file into the transcript.
func streamedOutput(streams bool, out string) string {
	if !streams {
		return ""
	}
	return out
}

// capOutput bounds a permanent output card.
func capOutput(s string) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= maxToolOutBytes {
		return s
	}
	// Same reasoning as the live view: the end carries the answer.
	return "…\n" + s[len(s)-maxToolOutBytes:]
}

// chunkInterval is how often live tool output is pushed to the message loop.
const chunkInterval = 60 * time.Millisecond

// chunkGate batches streaming tool output into the TUI message loop.
//
// A build that prints ten thousand lines would otherwise post ten thousand
// messages, each of which re-renders the block and re-syncs the viewport. That
// is not slow progress, it is a frozen terminal and a CPU at 100%, which is the
// exact failure live output was added to avoid.
//
// Chunks are batched, not dropped: whatever arrived since the last push is sent
// together, so no output is lost and the live view simply lags by up to one
// interval. A dropped chunk would leave a gap in the middle of a build log, and
// a gap in a build log is worse than a slightly late one.
type chunkGate struct {
	// post delivers a message. A function rather than a *tea.Program so the gate
	// can be driven without a running message loop - which is the only way to
	// assert on batching, and batching is the entire reason this type exists.
	post func(toolChunkMsg)

	mu    sync.Mutex
	last  time.Time
	held  string
	names map[string]bool
}

// newChunkGate returns a gate that posts to prog, or nil when prog is nil.
//
// A nil gate is a working no-op, so the caller does not have to check: without
// a program there is no message loop and nothing to post to.
func newChunkGate(prog *tea.Program) *chunkGate {
	if prog == nil {
		return nil
	}
	return &chunkGate{
		post:  func(m toolChunkMsg) { prog.Send(m) },
		last:  time.Now(),
		names: map[string]bool{},
	}
}

// emit records chunk and posts the accumulated output if the interval has passed.
func (g *chunkGate) emit(name, chunk string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.held += chunk
	// Bounded while held, not just when sent: a tool that prints megabytes
	// between two interval boundaries would otherwise be held whole in memory.
	if len(g.held) > maxLiveToolBytes {
		g.held = g.held[len(g.held)-maxLiveToolBytes:]
	}
	// One gate serves every tool in a turn, and parallel tools interleave. The
	// names are tracked so a batch knows which tools contributed, and the live
	// view is told the truth rather than attributing one tool's output to
	// whichever happened to be running.
	g.names[name] = true
	now := time.Now()
	if now.Sub(g.last) < chunkInterval {
		g.mu.Unlock()
		return
	}
	g.last = now
	held, names := g.held, gateNames(g.names)
	g.held = ""
	g.mu.Unlock()

	g.post(toolChunkMsg{tool: names, chunk: held})
}

// flush posts whatever is held, used when a call finishes so the last lines
// before completion are not left waiting for an interval that will not come.
func (g *chunkGate) flush() {
	if g == nil {
		return
	}
	g.mu.Lock()
	held, names := g.held, gateNames(g.names)
	g.held = ""
	g.names = map[string]bool{}
	g.mu.Unlock()
	if held == "" {
		return
	}
	g.post(toolChunkMsg{tool: names, chunk: held})
}

// gateNames renders the set of tools that contributed, for the live header.
func gateNames(set map[string]bool) string {
	if len(set) == 1 {
		for n := range set {
			return n
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	// Sorted so the header does not reshuffle between renders, which reads as
	// flickering even when nothing changed.
	sort.Strings(out)
	return strings.Join(out, "+")
}
