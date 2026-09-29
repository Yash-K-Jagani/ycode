package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// The input box.
//
// It was a bare textarea with no keymap, a fixed height of three, and Enter
// hardwired to submit - which made a multi-line prompt unreachable, since
// there was no other way to put a newline in. For a coding assistant that is
// the wrong shape: pasting a stack trace, a function signature, or a diff all
// arrive as several lines, and every one of them had to be joined by hand.
//
// Three things are here: a real multiline binding, a history that remembers
// what you asked, and the editing keys a text editor is expected to have. None
// of them steal a key that ordinary typing uses, because the up and down arrows
// are only taken when the cursor is on the first or last line, so a multi-line
// prompt can still be navigated.

const (
	// inputMinHeight is one row of prompt plus nothing; inputMaxHeight stops a
	// pasted log from eating the transcript.
	inputMinHeight = 1
	inputMaxHeight = 10
	// inputHistoryMax is how many past prompts are kept. A week of work is
	// comfortably inside it, and it is per-run rather than persisted, so a
	// fresh start is a fresh start.
	inputHistoryMax = 200
)

// newTextarea builds the prompt box with its keymap and sizing.
//
// It is a constructor rather than a block in New for the same reason
// newViewport is: a test that builds a Model by hand has to get the same input
// box as production, or the tests describe a component nobody runs.
func newTextarea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything…  (/help, Tab modes, @file to attach, Alt+Enter for a new line)"
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetHeight(inputMinHeight)
	ta.ShowLineNumbers = false
	// The defaults are a text editor's, and half of them are single-key
	// ctrl combinations that people reach for reflexively. The ones that are
	// not already bound in a modern editor are added below, in the keymap
	// rather than in the update loop, so they behave the same everywhere.
	km := ta.KeyMap
	km.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "new line"),
	)
	km.DeleteBeforeCursor = key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "clear line"),
	)
	km.DeleteWordBackward = key.NewBinding(
		key.WithKeys("ctrl+w", "alt+backspace"),
		key.WithHelp("ctrl+w", "delete word"),
	)
	km.LineStart = key.NewBinding(key.WithKeys("ctrl+a", "home"))
	km.LineEnd = key.NewBinding(key.WithKeys("ctrl+e", "end"))
	ta.KeyMap = km
	// LineNext and LinePrevious default to down and up, which is right inside a
	// multi-line prompt. They are cleared here because up and down are what the
	// history uses when the cursor is on an edge, and a key can only be one
	// thing: history navigation at the boundary, line movement in the middle.
	km2 := ta.KeyMap
	km2.LineNext = key.NewBinding(key.WithKeys("ctrl+n"))
	km2.LinePrevious = key.NewBinding(key.WithKeys("ctrl+p"))
	ta.KeyMap = km2
	return ta
}

// inputHistory is the list of submitted prompts, most recent first.
type inputHistory struct {
	entries []string
	// draft holds what was typed before a history walk began, so arrow-down
	// past the newest entry restores it instead of leaving the box empty.
	draft string
	// idx is the position being previewed: len(entries) means "not walking",
	// and otherwise counts from the end so new entries do not renumber it.
	idx int
}

func (h *inputHistory) add(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	// Do not record the same prompt twice running; people do repeat themselves
	// when a turn fails, and the history is for recalling, not for counting.
	if len(h.entries) > 0 && h.entries[0] == s {
		return
	}
	h.entries = append([]string{s}, h.entries...)
	if len(h.entries) > inputHistoryMax {
		h.entries = h.entries[:inputHistoryMax]
	}
	h.idx = len(h.entries)
	h.draft = ""
}

// previous moves one entry toward the older prompts, returning it and whether
// there was one.
//
// Entries are stored newest first, so "older" is a *larger* index. Getting this
// backwards is invisible on a single entry and wrong on every one after it, so
// it is worth stating rather than leaving to the reader.
//
// draft is whatever is in the box right now. It is saved on the first step so
// that walking back off the newest entry can put it back - the history has no
// other way to learn it, and a version that stored an empty string silently
// threw away the half-written question the person was composing.
func (h *inputHistory) previous(draft string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if h.idx == len(h.entries) {
		h.draft = draft
		h.idx = 0
		return h.entries[0], true
	}
	if h.idx+1 < len(h.entries) {
		h.idx++
		return h.entries[h.idx], true
	}
	// Already at the oldest; stay put.
	return h.entries[h.idx], true
}

// next moves one entry back toward the newest, and off the end restores the
// draft that was being typed before the walk began.
func (h *inputHistory) next() (string, bool) {
	if h.idx <= 0 || h.idx >= len(h.entries) {
		return "", false
	}
	h.idx--
	if h.idx == 0 {
		// Leaving the oldest entry lands back on the newest, and one more
		// step restores the draft.
		return h.entries[0], true
	}
	return h.entries[h.idx], true
}

// restore is the step past the newest entry, which gives back the draft.
func (h *inputHistory) restore() (string, bool) {
	if h.idx != 0 || len(h.entries) == 0 {
		return "", false
	}
	h.idx = len(h.entries)
	return h.draft, true
}

func (h *inputHistory) reset() { h.idx = len(h.entries) }

// fitsHeight is how many rows the prompt needs for its content, clamped.
//
// The box used to be a fixed three rows, which meant a one-word question
// occupied a third of the screen and a pasted log was scrolled inside a
// three-row window with no way to see it.
func inputHeight(ta textarea.Model) int {
	n := ta.LineCount()
	if n < inputMinHeight {
		return inputMinHeight
	}
	if n > inputMaxHeight {
		return inputMaxHeight
	}
	return n
}

// handleInputKey deals with the keys that are about the prompt box rather than
// about a mode, and reports whether it took the message.
//
// It runs before the global key switch, so a key bound here never also reaches
// the mode handling - which matters for Enter, since it both submits and
// inserts a newline depending on a modifier.
//
// resized is true when the key may have changed the content, which is what
// triggers a re-measure of the box. Without it the prompt only ever grew at
// the boundaries below, and stayed three rows tall for ordinary typing.
func (m *Model) handleInputKey(msg tea.KeyMsg) (handled, resized bool) {
	switch msg.String() {
	case "alt+enter", "ctrl+j":
		// A newline in the prompt, not a submission. This is what makes a
		// multi-line question possible at all.
		if m.ta.Focused() {
			m.ta.InsertString("\n")
			return true, true
		}
		return true, false

	case "up":
		// Only at the first line: inside a multi-line prompt the arrow still
		// moves the cursor, which is what someone editing three lines expects.
		if m.ta.Focused() && m.ta.Line() == 0 {
			if v, ok := m.hist.previous(m.ta.Value()); ok {
				m.ta.SetValue(v)
				m.ta.CursorEnd()
				return true, true
			}
		}
		return false, false

	case "down":
		if m.ta.Focused() && m.ta.Line() >= m.ta.LineCount()-1 {
			// Two steps: back toward the newest, and off the end into the
			// draft. A single step cannot express both, and the draft is the
			// one that must not be lost.
			v, ok := m.hist.next()
			if !ok {
				v, ok = m.hist.restore()
			}
			if ok {
				m.ta.SetValue(v)
				m.ta.CursorEnd()
				return true, true
			}
		}
		return false, false
	}
	return false, false
}

// resizeInput grows the prompt box to its content, within limits.
func (m *Model) resizeInput() { m.ta.SetHeight(inputHeight(m.ta)) }
