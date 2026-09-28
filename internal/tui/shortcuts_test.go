package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// Ctrl+P and Ctrl+R were declared in the keymap and listed in
// docs/shortcuts.md, but nothing dispatched them: pressing either did nothing
// at all. They are wired now, and these tests are what stop them silently
// becoming dead again.

func newKeyModel(t *testing.T) *Model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on unix
	ta := textarea.New()
	ta.SetHeight(1)
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.ActiveModel = "test-model"
	// Nothing listens here, so nothing can reach a real model list.
	cfg.OllamaHost = "http://127.0.0.1:1"
	return &Model{
		cfg:     cfg,
		router:  router.New(cfg),
		mode:    modes.Build,
		workdir: t.TempDir(),
		toolreg: tools.DefaultRegistry(t.TempDir()),
		hookset: hooks.LoadFiles(nil),
		ta:      ta,
		sess:    sessions.New(cfg.ActiveProvider, cfg.ActiveModel),
	}
}

func TestCtrlPOpensThePalette(t *testing.T) {
	m := newKeyModel(t)
	m.ta.SetValue("hello")

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	got := out.(*Model)

	// The palette is a completion popup keyed off the input, so a seeded "/"
	// is what opens it.
	if v := got.ta.Value(); v != "/" {
		t.Fatalf("input = %q, want %q", v, "/")
	}
	if got.palHide {
		t.Fatal("the palette was hidden")
	}
	// And it must actually have something to show.
	if items := got.paletteItems(); len(items) == 0 {
		t.Fatal("the palette opened with nothing in it")
	}
}

func TestCtrlRDispatchesSessions(t *testing.T) {
	m := newKeyModel(t)
	// No saved sessions in a temp home, so the handler answers rather than
	// opening a picker. What matters is that it ran at all.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := out.(*Model)
	if got.sessionsModal != nil {
		t.Skip("this environment has saved sessions, so a picker opened instead")
	}
	if len(got.msgs) == 0 {
		t.Fatal("ctrl+r did not dispatch /sessions")
	}
	if !strings.Contains(strings.Join(got.msgs, "\n"), "session") {
		t.Fatalf("unexpected reply: %v", got.msgs)
	}
}

// Ctrl+O is already a no-op while streaming; the new keys must match, or they
// would open a picker or retarget the input mid-turn.
func TestShortcutsAreInertWhileBusy(t *testing.T) {
	for name, key := range map[string]tea.KeyMsg{
		"ctrl+p": {Type: tea.KeyCtrlP},
		"ctrl+r": {Type: tea.KeyCtrlR},
	} {
		t.Run(name, func(t *testing.T) {
			m := newKeyModel(t)
			m.busy = true
			m.ta.SetValue("untouched")
			out, cmd := m.Update(key)
			if cmd != nil {
				t.Fatalf("%s started work while busy", name)
			}
			got := out.(*Model)
			if got.ta.Value() != "untouched" {
				t.Fatalf("%s edited the input while busy: %q", name, got.ta.Value())
			}
			if got.sessionsModal != nil {
				t.Fatalf("%s opened a picker while busy", name)
			}
		})
	}
}
