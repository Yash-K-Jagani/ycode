package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/store"
)

// The three pickers share their selection and clamping logic, so it is tested
// once here and then exercised through each modal.

func TestMoveSelDoesNotWrap(t *testing.T) {
	// Wrapping would make it easy to commit the wrong model by holding a key.
	if got := moveSel(0, -1, 3); got != 0 {
		t.Fatalf("up at the top moved to %d", got)
	}
	if got := moveSel(2, 1, 3); got != 2 {
		t.Fatalf("down at the bottom moved to %d", got)
	}
	if got := moveSel(0, 1, 3); got != 1 {
		t.Fatalf("down moved to %d", got)
	}
	if got := moveSel(2, -1, 3); got != 1 {
		t.Fatalf("up moved to %d", got)
	}
	// An empty list has nowhere to go, and must not return -1.
	if got := moveSel(0, 1, 0); got != 0 {
		t.Fatalf("down in an empty list gave %d", got)
	}
	if got := moveSel(0, -1, 0); got != 0 {
		t.Fatalf("up in an empty list gave %d", got)
	}
}

func TestClampSel(t *testing.T) {
	for _, tc := range []struct{ sel, n, want int }{
		{0, 3, 0}, {2, 3, 2}, {5, 3, 2}, {-1, 3, 0}, {0, 0, 0}, {7, 0, 0},
	} {
		if got := clampSel(tc.sel, tc.n); got != tc.want {
			t.Fatalf("clampSel(%d, %d) = %d, want %d", tc.sel, tc.n, got, tc.want)
		}
	}
}

func TestPickKeyIgnoresOtherMessages(t *testing.T) {
	sel := 1
	pickKey(&sel, 3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if sel != 1 {
		t.Fatalf("a letter key moved the selection to %d", sel)
	}
	pickKey(&sel, 3, struct{}{})
	if sel != 1 {
		t.Fatalf("a non-key message moved the selection to %d", sel)
	}
}

func testModelEntries() []modelEntry {
	return []modelEntry{
		{Provider: "ollama", Model: "qwen2.5-coder:3b", Installed: true},
		{Provider: "ollama", Model: "llama3:8b", Installed: true},
		{Provider: "gemini", Model: "gemini-2.5-pro"},
	}
}

func TestModelsModalFiltersAndClamps(t *testing.T) {
	m := newModelsModal(testModelEntries(), "")

	// Arrow keys move the selection within the full list.
	m.update(tea.KeyMsg{Type: tea.KeyDown})
	if m.sel != 1 {
		t.Fatalf("down gave sel = %d", m.sel)
	}
	m.update(tea.KeyMsg{Type: tea.KeyUp})
	if m.sel != 0 {
		t.Fatalf("up gave sel = %d", m.sel)
	}

	// Typing narrows the list, and the selection cannot point past the end -
	// Enter would otherwise commit filtered[3] of a one-item list.
	m.input.SetValue("gemini")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 1 {
		t.Fatalf("filtered to %d entries", len(m.filtered))
	}
	if m.sel != 0 {
		t.Fatalf("sel = %d with one match", m.sel)
	}

	// A query that matches nothing must not leave a selection that panics on
	// commit.
	m.input.SetValue("zzzznomatch")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 0 {
		t.Fatalf("expected no matches, got %d", len(m.filtered))
	}
	if m.sel != 0 {
		t.Fatalf("sel = %d with no matches", m.sel)
	}

	// Clearing the filter restores everything.
	m.input.SetValue("")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != len(m.entries) {
		t.Fatalf("clearing the filter gave %d of %d", len(m.filtered), len(m.entries))
	}
}

func TestModelsModalRenders(t *testing.T) {
	m := newModelsModal(testModelEntries(), "ollama unreachable")
	out := m.view(60, lipgloss.Color("#fff"))
	if out == "" {
		t.Fatal("the modal rendered nothing")
	}
	for _, want := range []string{"qwen2.5-coder:3b", "ollama unreachable"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Fatalf("the modal does not show %q", want)
		}
	}
	// A very narrow terminal must not produce a broken or empty panel.
	if small := m.view(5, lipgloss.Color("#fff")); small == "" {
		t.Fatal("a narrow terminal rendered nothing")
	}
	// An empty list is a state the user can reach by filtering.
	m.input.SetValue("zzzz")
	m.update(tea.KeyMsg{})
	if out := m.view(60, lipgloss.Color("#fff")); out == "" {
		t.Fatal("an empty modal rendered nothing")
	}
}

func TestSessionsModalFiltersTitleAndModel(t *testing.T) {
	list := []sessions.Session{
		{ID: "a", Title: "add healthcheck", Provider: "ollama", Model: "qwen"},
		{ID: "b", Title: "refactor router", Provider: "gemini", Model: "flash"},
	}
	m := newSessionsModal(list)

	m.input.SetValue("router")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 1 || m.filtered[0].ID != "b" {
		t.Fatalf("filtering by title gave %+v", m.filtered)
	}

	// The provider is searchable too, which is how a user finds a session run
	// against a model they no longer use.
	m.input.SetValue("gemini")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 1 || m.filtered[0].ID != "b" {
		t.Fatalf("filtering by provider gave %+v", m.filtered)
	}

	// The id is searchable as well.
	m.input.SetValue("a")
	m.update(tea.KeyMsg{})
	if len(m.filtered) == 0 {
		t.Fatal("filtering by id found nothing")
	}

	if out := m.view(60, lipgloss.Color("#fff")); !strings.Contains(stripANSI(out), "Sessions") {
		t.Fatalf("the sessions modal is missing its title: %q", stripANSI(out))
	}
}

func TestStoreModalFiltersAndRenders(t *testing.T) {
	list := []store.Entry{
		{Name: "react-scaffold", Kind: "skill", Description: "start a react app"},
		{Name: "fastapi-scaffold", Kind: "skill", Description: "start a python api"},
	}
	m := newStoreModal(list)

	m.input.SetValue("fastapi")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 1 || m.filtered[0].Name != "fastapi-scaffold" {
		t.Fatalf("filtering gave %+v", m.filtered)
	}

	// The description is searchable, so a user can search for what they want
	// rather than what it is called.
	m.input.SetValue("python api")
	m.update(tea.KeyMsg{})
	if len(m.filtered) != 1 {
		t.Fatalf("filtering by description gave %+v", m.filtered)
	}

	m.input.SetValue("")
	m.update(tea.KeyMsg{})
	if out := m.view(60, lipgloss.Color("#fff")); !strings.Contains(stripANSI(out), "react-scaffold") {
		t.Fatalf("the store modal is missing an entry: %q", stripANSI(out))
	}
}

// An empty list is a real state - /sessions with nothing saved, the store
// offline - and must render rather than panic.
func TestModalsHandleEmptyLists(t *testing.T) {
	accent := lipgloss.Color("#fff")
	if out := newModelsModal(nil, "").view(60, accent); out == "" {
		t.Error("an empty models modal rendered nothing")
	}
	if out := newSessionsModal(nil).view(60, accent); out == "" {
		t.Error("an empty sessions modal rendered nothing")
	}
	if out := newStoreModal(nil).view(60, accent); out == "" {
		t.Error("an empty store modal rendered nothing")
	}
}
