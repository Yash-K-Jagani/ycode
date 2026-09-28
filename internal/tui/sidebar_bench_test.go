package tui

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
)

// The sidebar runs on every render, and bubbletea renders after every message -
// including every streamed token. It reads the todo file and the batch queue,
// so this is the cost of a keystroke or a chunk of output.
func BenchmarkSidebar(b *testing.B) {
	// Not b.TempDir(): db.Shared() keeps a SQLite handle open for the
	// process, and t.TempDir cleanup would fail on it and swallow the
	// benchmark result.
	home, err := os.MkdirTemp("", "ycode-bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	b.Setenv("USERPROFILE", home)
	b.Setenv("HOME", home)
	ta := textarea.New()
	ta.SetHeight(1)
	cfg := config.Defaults()
	workdir := b.TempDir()
	m := &Model{
		cfg:     cfg,
		mode:    "build",
		workdir: workdir,
		toolreg: tools.DefaultRegistry(workdir),
		tracker: cost.New(),
		hookset: hooks.LoadFiles(nil),
		ta:      ta,
		vp:      viewport.New(80, 20),
		th:      theme.Dark(),
		sess:    sessions.New(cfg.ActiveProvider, cfg.ActiveModel),
	}
	// Write a few tasks so the benchmark measures the steady state rather
	// than the empty case.
	tt := &tools.TodoTool{Workdir: workdir}
	for _, text := range []string{"do the thing", "do the other thing"} {
		if _, err := tt.Run(context.Background(), json.RawMessage(`{"action":"add","text":"`+text+`"}`)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.sidebar(20)
	}
}
