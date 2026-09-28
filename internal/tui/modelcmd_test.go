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
// A total failure must name what was tried and why each gave up. The old
// message was the bare string "all providers failed", and with fallbacks
// configured the user saw only the final error, with no provider or model in
// it — useless precisely when they need to know whether to fix a key or switch
// models.
func TestAllProvidersFailedNamesEveryAttempt(t *testing.T) {
	if got := allProvidersFailed(nil).Error(); got != "no provider was available for this turn — check /status and /connect" {
		t.Fatalf("empty case = %q", got)
	}
	err := allProvidersFailed([]string{
		"ollama/qwen2.5-coder:3b: dial tcp 127.0.0.1:11434: connection refused",
		"gemini/gemini-2.0-flash: 401 unauthorized",
	})
	msg := err.Error()
	if !strings.Contains(msg, "every provider failed") {
		t.Fatalf("missing summary: %q", msg)
	}
	for _, want := range []string{
		"ollama/qwen2.5-coder:3b",
		"connection refused",
		"gemini/gemini-2.0-flash",
		"401 unauthorized",
		"fallbacks were tried",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in:\n%s", want, msg)
		}
	}
	// A single attempt should not suggest fallbacks were involved.
	single := allProvidersFailed([]string{"ollama/x: boom"}).Error()
	if strings.Contains(single, "fallbacks were tried") {
		t.Fatalf("single attempt should not mention fallbacks: %q", single)
	}
}

// The allow-list filter and the agent loop's central check read the same
// table now; this guards the wiring. It previously hardcoded exactly two names
// (browser, github) while nine more tools could reach the network.
func TestFilterNetworkToolsMatchesPolicy(t *testing.T) {
	in := []string{
		"read", "api", "write", "browser", "github", "git", "db", "notebook",
		"scaffold", "vscode", "mcp__srv__tool", "plugin__mine", "grep", "todo",
		"run", "bash", "testgen",
	}
	got := filterNetworkTools(in)
	for _, n := range got {
		if tools.ReachesNetwork(n) {
			t.Fatalf("%s reaches the network but survived the filter", n)
		}
	}
	wantLocal := []string{"read", "write", "grep", "todo", "run", "bash", "testgen"}
	for _, n := range wantLocal {
		found := false
		for _, g := range got {
			if g == n {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is local but was filtered out: %v", n, got)
		}
	}
	for _, n := range []string{"api", "browser", "github", "git", "db", "notebook", "scaffold", "vscode", "mcp__srv__tool", "plugin__mine"} {
		for _, g := range got {
			if g == n {
				t.Fatalf("%s reaches the network but survived the filter", n)
			}
		}
	}
}

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
