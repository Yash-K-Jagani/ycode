package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
)

// View is the one function whose failure takes the process down rather than
// showing a wrong thing, and it reads almost every field on the Model. A
// partially-built Model is easy to reach in practice: a modal that was closed
// early, a turn that ended without a session message, a goal that was cleared
// between two renders.

func realModel(t *testing.T) *Model {
	t.Helper()
	tempHome(t)
	cfg := config.Defaults()
	cfg.OllamaHost = "http://127.0.0.1:1"
	m := New(cfg, router.New(cfg), sessions.New(cfg.ActiveProvider, cfg.ActiveModel), t.TempDir())
	return &m
}

func TestViewRendersEveryMode(t *testing.T) {
	for _, md := range modes.All() {
		t.Run(string(md), func(t *testing.T) {
			m := realModel(t)
			m.mode = md
			out := m.View()
			if strings.TrimSpace(stripANSI(out)) == "" {
				t.Fatalf("%s rendered nothing", md)
			}
			// The mode chip tells the user which mode they are in; showing the
			// wrong one is worse than showing none.
			if !strings.Contains(stripANSI(out), string(md)) {
				t.Fatalf("%s: the chip does not name the mode", md)
			}
		})
	}
}

func TestViewWithSidebarAndFileTree(t *testing.T) {
	m := realModel(t)
	m.sideOn = true
	m.showTree = true
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if out := m.View(); strings.TrimSpace(stripANSI(out)) == "" {
		t.Fatal("rendering with the sidebar and file tree produced nothing")
	}
	// Both panels on, and both off, must render.
	m.sideOn = false
	m.showTree = false
	if out := m.View(); strings.TrimSpace(stripANSI(out)) == "" {
		t.Fatal("rendering with no panels produced nothing")
	}
}

func TestViewWithZeroDataLeakBadge(t *testing.T) {
	m := realModel(t)
	// The badge is the visible half of a privacy promise; if it is missing,
	// the user has no way to know the mode is on.
	m.cfg.ZeroDataLeak = true
	if out := stripANSI(m.View()); !strings.Contains(out, "LOCAL ONLY") {
		t.Fatalf("no LOCAL ONLY badge: %q", out)
	}
	m.cfg.ZeroDataLeak = false
	if out := stripANSI(m.View()); strings.Contains(out, "LOCAL ONLY") {
		t.Fatal("the badge is shown when the mode is off")
	}
}

func TestViewWithAStreamingTurn(t *testing.T) {
	m := realModel(t)
	m.busy = true
	m.mode = modes.Build
	m.stream.WriteString("partially streamed ans")
	m.appendSys("a finished message")
	if out := strings.TrimSpace(stripANSI(m.View())); out == "" {
		t.Fatal("rendering mid-stream produced nothing")
	}
}

func TestViewWithAGoal(t *testing.T) {
	m := realModel(t)
	m.mode = modes.Goal
	m.goal = goal.New("add a healthcheck endpoint", 12)
	m.goal.Iter = 3
	if out := stripANSI(m.View()); !strings.Contains(out, "3/12") {
		t.Fatalf("the goal counter is missing: %q", out)
	}

	// A finished goal still has to render, with its status shown.
	m.goal.Status = goal.Met
	if out := strings.TrimSpace(stripANSI(m.View())); out == "" {
		t.Fatal("rendering a finished goal produced nothing")
	}
}

func TestViewWithToastsAndAttachments(t *testing.T) {
	m := realModel(t)
	m.toasts = []toast{{msg: "something happened", until: time.Now().Add(time.Minute)}}
	if out := stripANSI(m.View()); !strings.Contains(out, "something happened") {
		t.Fatalf("the toast is not shown: %q", out)
	}
}

// Every message Update handles should leave the Model renderable. This is the
// cheap way to catch a handler that forgets to initialise something View needs.
//
// errMsg{} carries no error, which no sender currently produces; it is here
// because the handler used to dereference it unconditionally, and a panic
// inside Update kills the session rather than showing a wrong thing.
func TestViewSurvivesEveryMessage(t *testing.T) {
	msgs := []tea.Msg{
		spinner.TickMsg{},
		toastExpireMsg{},
		tea.WindowSizeMsg{Width: 100, Height: 30},
		deltaMsg("thinking"),
		toolMsg{name: "write", status: "ok"},
		sysMsg("note to self"),
		doneMsg{mode: modes.Build, text: "done", calls: 1, oks: 1, work: 1},
		errMsg{},
	}
	for _, msg := range msgs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Update(%T) then View panicked: %v", msg, r)
				}
			}()
			m := realModel(t)
			m.Update(msg)
			_ = m.View()
		}()
	}
}

func TestInitAndSetProgram(t *testing.T) {
	m := realModel(t)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no command, so the cursor never blinks")
	}
	// SetProgram is called once at startup; a nil program must be tolerated,
	// because handlers check it before quitting.
	m.SetProgram(nil)
	m.SetProgram(nil)
}
