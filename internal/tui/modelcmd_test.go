package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
)

// newModelCmd builds a minimal Model for command/Update tests.
func newModelCmd(t *testing.T) *Model {
	t.Helper()
	ta := textarea.New()
	ta.SetHeight(1)
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.ActiveModel = "first-model"
	// Nothing listens here, so the command cannot reach a real model list.
	cfg.OllamaHost = "http://127.0.0.1:1"
	return &Model{
		cfg:     cfg,
		router:  router.New(cfg),
		mode:    modes.Build,
		workdir: t.TempDir(),
		toolreg: tools.DefaultRegistry(t.TempDir()),
		tracker: cost.New(),
		hookset: hooks.LoadFiles(nil),
		ta:      ta,
		vp:      viewport.New(80, 20),
		th:      theme.For("dark"),
		sess:    sessions.New("ollama", "first-model"),
	}
}

// Ctrl+O used to apply the model switch inside its Cmd goroutine, writing
// m.cfg, m.sess and router.cfg while the turn goroutine and View() read them.
// The command must now only *propose* a model; the UI thread applies it.
func TestModelSwitchIsAppliedOnTheUIThread(t *testing.T) {
	m := newModelCmd(t)
	// Unreachable host: the command must return without touching config.
	cmd := m.cycleModelCmd()
	if cmd == nil {
		t.Fatal("cycleModelCmd returned nil")
	}
	before := m.cfg.ActiveModel
	msg := cmd()
	if _, ok := msg.(modelSwitchedMsg); ok {
		t.Fatal("an unreachable ollama host must not produce a switch message")
	}
	if m.cfg.ActiveModel != before {
		t.Fatalf("running the command mutated config: %q -> %q", before, m.cfg.ActiveModel)
	}
	if m.sess.Model != before {
		t.Fatalf("running the command mutated the session: %q", m.sess.Model)
	}
}

// Applying the message on the UI thread is what actually switches models.
func TestModelSwitchedMsgAppliesOnUpdate(t *testing.T) {
	m := newModelCmd(t)
	out, _ := m.Update(modelSwitchedMsg{model: "second-model"})
	got, ok := out.(*Model)
	if !ok {
		t.Fatal("Update returned a non-Model")
	}
	if got.cfg.ActiveModel != "second-model" {
		t.Fatalf("ActiveModel = %q, want second-model", got.cfg.ActiveModel)
	}
	if got.sess.Model != "second-model" {
		t.Fatalf("session model = %q, want second-model", got.sess.Model)
	}
	if !strings.Contains(strings.Join(got.msgs, "\n"), "Switched to ollama / second-model") {
		t.Fatalf("no confirmation shown: %v", got.msgs)
	}
}

// A switch that arrives after the user started a turn is dropped: the running
// turn is already streaming against the previous model.
func TestModelSwitchIgnoredWhileBusy(t *testing.T) {
	m := newModelCmd(t)
	m.busy = true
	out, _ := m.Update(modelSwitchedMsg{model: "second-model"})
	got := out.(*Model)
	if got.cfg.ActiveModel != "first-model" {
		t.Fatalf("ActiveModel changed while busy: %q", got.cfg.ActiveModel)
	}
}

// Ctrl+O must be a no-op while a turn is streaming, like the other keys.
func TestCtrlOIgnoredWhileBusy(t *testing.T) {
	m := newModelCmd(t)
	m.busy = true
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd != nil {
		t.Fatal("ctrl+o started a command while busy")
	}
	if out.(*Model).cfg.ActiveModel != "first-model" {
		t.Fatal("ctrl+o changed the model while busy")
	}
}
