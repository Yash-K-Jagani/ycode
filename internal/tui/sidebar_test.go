package tui

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// The sidebar renders on every message, and a streaming turn sends one per
// token. It used to read the todo file and query the batch table every time,
// which measured at 547µs per render. The cache makes that a lookup, and this
// test pins both halves of the deal: it does not re-read while text streams in,
// and it does re-read once something can actually have changed.

// tempHome points config.Dir() at a throwaway directory for the test.
//
// It deliberately avoids t.TempDir(): db.Shared() opens one SQLite handle for
// the whole process and never closes it, so t.TempDir's cleanup would fail on
// the locked database and report the test as failed even when every assertion
// passed. Cleanup here is best-effort, and only the locked file survives.
func tempHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "ycode-tui-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on unix
	return home
}

func sidebarModel(t *testing.T) (*Model, string) {
	t.Helper()
	tempHome(t)
	workdir := t.TempDir()
	m := newKeyModel(t)
	m.workdir = workdir
	return m, workdir
}

func addTodo(t *testing.T, workdir, text string) {
	t.Helper()
	tt := &tools.TodoTool{Workdir: workdir}
	if _, err := tt.Run(context.Background(), json.RawMessage(`{"action":"add","text":"`+text+`"}`)); err != nil {
		t.Fatal(err)
	}
}

// ansi matches the escape sequences lipgloss emits, so rendered output can be
// matched on its text.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// countShownTasks counts the task rows in a rendered sidebar. Each renders as
// "[ ] 1. text" or "[x] 2. text", behind the panel's vertical border.
func countShownTasks(side string) int {
	n := 0
	for _, line := range strings.Split(side, "\n") {
		text := strings.TrimLeft(ansi.ReplaceAllString(line, ""), " │")
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			n++
		}
	}
	return n
}

func TestSidebarCachesTaskListUntilInvalidated(t *testing.T) {
	m, workdir := sidebarModel(t)
	addTodo(t, workdir, "first task")

	// The first render reads from disk.
	side := m.sidebar(30)
	if got := countShownTasks(side); got != 1 {
		t.Fatalf("first render showed %d tasks, want 1", got)
	}
	if m.side.at.IsZero() {
		t.Fatal("the cache was not populated")
	}

	// A task added behind the sidebar's back is not picked up mid-stream. That
	// is the deliberate trade: bounded staleness beats a file read per token.
	addTodo(t, workdir, "second task")
	if got := countShownTasks(m.sidebar(30)); got != 1 {
		t.Fatalf("the cache was bypassed: %d tasks", got)
	}

	// Invalidating is what a tool result does, and the new task appears.
	m.invalidateSidebar()
	if got := countShownTasks(m.sidebar(30)); got != 2 {
		t.Fatalf("after invalidating, %d tasks, want 2", got)
	}
}

// A turn ends with the task list changed, so doneMsg must drop the cache even
// though no single tool message for the last step was seen.
func TestSidebarRefreshesAfterATurnEnds(t *testing.T) {
	m, workdir := sidebarModel(t)
	_ = m.sidebar(30)
	addTodo(t, workdir, "written during the turn")

	if _, cmd := m.Update(doneMsg{mode: m.mode, text: "done", calls: 1, oks: 1, work: 1}); cmd != nil {
		t.Fatal("doneMsg started another turn")
	}
	if !m.side.at.IsZero() {
		t.Fatal("doneMsg did not invalidate the sidebar cache")
	}
	if got := countShownTasks(m.sidebar(30)); got != 1 {
		t.Fatalf("after the turn, %d tasks, want 1", got)
	}
}

// The TTL exists so a batch queued by another process still shows up without
// per-token I/O. Expire it by hand rather than sleeping.
func TestSidebarCacheExpires(t *testing.T) {
	m, workdir := sidebarModel(t)
	addTodo(t, workdir, "first task")
	_ = m.sidebar(30)
	addTodo(t, workdir, "second task")

	// Backdate the cache past the TTL.
	m.side.at = time.Now().Add(-2 * sidebarTTL)
	if got := countShownTasks(m.sidebar(30)); got != 2 {
		t.Fatalf("an expired cache did not re-read: %d tasks", got)
	}
}

// Repeated renders must not repeat the reads, which is the whole point.
func TestSidebarRepeatedRendersDoNotReRead(t *testing.T) {
	m, workdir := sidebarModel(t)
	addTodo(t, workdir, "only task")

	first := m.sidebar(30)
	at := m.side.at
	for i := 0; i < 50; i++ {
		if got := m.sidebar(30); got != first {
			t.Fatalf("render %d differed from the first", i)
		}
	}
	if !m.side.at.Equal(at) {
		t.Fatal("the cache timestamp moved, so something re-read the disk")
	}
}
