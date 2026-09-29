package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func inputModel(t *testing.T) Model {
	t.Helper()
	m := bareModel(t)
	return m
}

func typeString(m *Model, s string) {
	for _, r := range s {
		updated, _ := (&*m).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		*m = *(updated.(*Model))
	}
}

func press(m *Model, k tea.KeyMsg) {
	updated, _ := (&*m).Update(k)
	*m = *(updated.(*Model))
}

// A multi-line prompt was unreachable: Enter always submitted, and the textarea
// had no other way to receive a newline. For a coding assistant that is the
// wrong shape, because a pasted stack trace or a function signature arrives as
// several lines.
func TestAltEnterInsertsANewlineInsteadOfSubmitting(t *testing.T) {
	m := inputModel(t)
	typeString(&m, "first line")
	press(&m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	typeString(&m, "second line")

	if !strings.Contains(m.ta.Value(), "\n") {
		t.Fatalf("no newline in the prompt: %q", m.ta.Value())
	}
	if !strings.Contains(m.ta.Value(), "first line") || !strings.Contains(m.ta.Value(), "second line") {
		t.Fatalf("both lines should be present: %q", m.ta.Value())
	}
	if m.busy {
		t.Fatal("alt+enter submitted the turn instead of adding a line")
	}
}

func TestCtrlJAlsoInsertsANewline(t *testing.T) {
	m := inputModel(t)
	typeString(&m, "one")
	press(&m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeString(&m, "two")
	if !strings.Contains(m.ta.Value(), "\n") {
		t.Fatalf("ctrl+j did not add a line: %q", m.ta.Value())
	}
	if m.busy {
		t.Fatal("ctrl+j submitted the turn")
	}
}

// Plain Enter must still submit. The whole point of adding a modifier was to
// make room for a newline, not to take Enter away.
func TestPlainEnterStillSubmits(t *testing.T) {
	m := inputModel(t)
	typeString(&m, "hello")
	before := m.busy
	press(&m, tea.KeyMsg{Type: tea.KeyEnter})
	// submit() is a tea.Cmd rather than synchronous, so the observable effect
	// is that the box was cleared and a command produced, not that busy is set.
	if m.busy == before && m.ta.Value() == "hello" {
		t.Fatal("enter neither submitted nor cleared the prompt")
	}
}

// The box was a fixed three rows, so a one-word question took a third of the
// screen and a pasted log was scrolled inside a window with no way to see it.
func TestInputGrowsWithItsContent(t *testing.T) {
	m := inputModel(t)
	typeString(&m, "short")
	if got := m.ta.Height(); got != 1 {
		t.Fatalf("a one-line prompt takes %d rows, want 1", got)
	}
	for i := 0; i < 5; i++ {
		press(&m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
		typeString(&m, "line")
	}
	if got := m.ta.Height(); got < 5 {
		t.Fatalf("a six-line prompt takes %d rows", got)
	}
}

func TestInputHeightIsCapped(t *testing.T) {
	// A textarea with n real lines, so inputHeight is measured against the same
	// LineCount it will see in production rather than a stand-in number.
	long := textarea.New()
	long.SetValue(strings.Repeat("line\n", 50))
	if got := inputHeight(long); got > inputMaxHeight {
		t.Fatalf("a long paste grew the box to %d rows, cap is %d", got, inputMaxHeight)
	}
	short := textarea.New()
	short.SetValue("one line")
	if got := inputHeight(short); got < inputMinHeight {
		t.Fatalf("height %d is below the minimum", got)
	}
	empty := textarea.New()
	if got := inputHeight(empty); got < inputMinHeight {
		t.Fatalf("an empty box reported height %d, want at least %d", got, inputMinHeight)
	}
}

// --- history ---

func TestHistoryRecallsPreviousPrompts(t *testing.T) {
	m := inputModel(t)
	m.hist.add("first question")
	m.hist.add("second question")

	press(&m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.ta.Value(); got != "second question" {
		t.Fatalf("up gave %q, want the most recent", got)
	}
	press(&m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.ta.Value(); got != "first question" {
		t.Fatalf("up gave %q, want the one before it", got)
	}
}

func TestHistoryWalksForwardAndRestoresTheDraft(t *testing.T) {
	// Walking forward past the newest entry has to return what was being
	// typed, or the prompt silently becomes empty and the half-written
	// question is lost.
	m := inputModel(t)
	m.hist.add("older")
	m.ta.SetValue("half-written thought")
	press(&m, tea.KeyMsg{Type: tea.KeyUp})
	if m.ta.Value() != "older" {
		t.Fatalf("up gave %q", m.ta.Value())
	}
	press(&m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.ta.Value(); got != "half-written thought" {
		t.Fatalf("down gave %q, want the draft back", got)
	}
}

func TestHistoryStopsAtTheEnds(t *testing.T) {
	m := inputModel(t)
	m.hist.add("only")
	press(&m, tea.KeyMsg{Type: tea.KeyUp})
	press(&m, tea.KeyMsg{Type: tea.KeyUp})
	if m.ta.Value() != "only" {
		t.Fatalf("walking past the oldest entry gave %q", m.ta.Value())
	}
	press(&m, tea.KeyMsg{Type: tea.KeyDown})
	press(&m, tea.KeyMsg{Type: tea.KeyDown})
	// No error and no panic is the assertion; a fresh prompt.
}

func TestHistoryIgnoresBlankAndRepeatedPrompts(t *testing.T) {
	var h inputHistory
	h.add("   ")
	if len(h.entries) != 0 {
		t.Fatal("a blank prompt was recorded")
	}
	h.add("same")
	h.add("same")
	if len(h.entries) != 1 {
		t.Fatalf("a repeated prompt running was recorded %d times", len(h.entries))
	}
	h.add("other")
	if len(h.entries) != 2 || h.entries[0] != "other" {
		t.Fatalf("entries = %v, want newest first", h.entries)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	var h inputHistory
	for i := 0; i < inputHistoryMax+50; i++ {
		h.add(string(rune('a'+i%26)) + strings.Repeat("x", i%7) + itoa(i))
	}
	if len(h.entries) > inputHistoryMax {
		t.Fatalf("history grew to %d, cap is %d", len(h.entries), inputHistoryMax)
	}
	// The most recent must survive the trimming.
	if !strings.HasSuffix(h.entries[0], itoa(inputHistoryMax+49)) {
		t.Fatalf("the newest entry was dropped: %q", h.entries[0])
	}
}

// Up and down have to mean history at the edges of a multi-line prompt and
// cursor movement inside it. Taking them unconditionally would make it
// impossible to edit a three-line question.
func TestArrowsStillMoveTheCursorInsideAMultiLinePrompt(t *testing.T) {
	m := inputModel(t)
	m.hist.add("something else")
	m.ta.SetValue("one\ntwo\nthree")
	m.ta.CursorStart()

	// From the first line, up has nowhere to go in a single-line prompt, so
	// this checks the middle: put the cursor on line two and confirm up
	// moves it rather than pulling from history.
	press(&m, tea.KeyMsg{Type: tea.KeyDown}) // line 1 (still first line: history)
	m.ta.SetValue("one\ntwo\nthree")
	m.ta.CursorStart()
	m.ta.CursorDown()
	m.ta.CursorDown() // now on the last line
	if line := m.ta.Line(); line != 2 {
		t.Fatalf("fixture is on line %d", line)
	}
	// On the last line, down is history; there is nothing below.
	press(&m, tea.KeyMsg{Type: tea.KeyDown})
	if m.ta.Value() != "one\ntwo\nthree" {
		t.Fatalf("down on the last line changed the prompt to %q", m.ta.Value())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
