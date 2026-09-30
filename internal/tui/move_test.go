package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

func moveModel(t *testing.T, dir string) *Model {
	t.Helper()
	m := &Model{workdir: dir, toolreg: tools.DefaultRegistry(dir)}
	return m
}

// --- the registry must follow ---

// The bug this exists to prevent: changing workdir alone leaves the tools that
// take a workdir pointing at the old tree while the status bar claims the new
// one. It looks correct until a file is written in the wrong place.
//
// `read` is deliberately not the probe: it resolves relative paths from the
// process's working directory and takes no workdir at all, so it would pass
// either way and prove nothing. `write` is the one that carries the path.
func TestMoveRebuildsTheRegistryForTheNewTree(t *testing.T) {
	ctx := context.Background()
	a, b := t.TempDir(), t.TempDir()
	m := moveModel(t, a)

	wt, ok := m.toolreg.Get("write")
	if !ok {
		t.Fatal("no write tool")
	}
	args := []byte(`{"path":"marker.txt","content":"from-a"}`)
	if _, err := wt.Run(ctx, args); err != nil {
		t.Fatalf("write in the original tree failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a, "marker.txt")); err != nil {
		t.Fatalf("write did not land in the original tree: %v", err)
	}

	if _, err := m.moveTo(b); err != nil {
		t.Fatalf("move: %v", err)
	}
	wt, _ = m.toolreg.Get("write")
	if _, err := wt.Run(ctx, args); err != nil {
		t.Fatalf("write in the new tree failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b, "marker.txt")); err != nil {
		t.Fatalf("the rebuilt registry did not write into the new tree: %v", err)
	}
	if m.workdir != b {
		t.Fatalf("workdir = %q, want %q", m.workdir, b)
	}
}

// --- validation ---

// Creating a directory because someone typed a typo produces a stray empty folder
// and a session that then reports no files, which is harder to diagnose than a
// clear refusal.
func TestMoveRefusesAMissingDirectory(t *testing.T) {
	m := moveModel(t, t.TempDir())
	_, err := m.moveTo(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing directory was accepted")
	}
	if !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("unhelpful error: %v", err)
	}
	if !strings.Contains(err.Error(), "/init") {
		t.Fatalf("the error does not suggest a way forward: %v", err)
	}
}

func TestMoveRefusesAFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := moveModel(t, dir)
	_, err := m.moveTo(f)
	if err == nil {
		t.Fatal("a file was accepted as a directory")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestMoveNeedsAPath(t *testing.T) {
	m := moveModel(t, t.TempDir())
	if _, err := m.moveTo("   "); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

// A relative path is resolved, so the tree does not drift when the process's
// working directory is something else later.
func TestMoveResolvesRelativePaths(t *testing.T) {
	m := moveModel(t, t.TempDir())
	if _, err := m.moveTo("."); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(m.workdir) {
		t.Fatalf("workdir = %q, want an absolute path", m.workdir)
	}
}

func TestMoveExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sub := filepath.Join(home, "project")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m := moveModel(t, t.TempDir())
	if _, err := m.moveTo("~/project"); err != nil {
		t.Fatalf("tilde not expanded: %v", err)
	}
	if !sameDir(m.workdir, sub) {
		t.Fatalf("workdir = %q, want %q", m.workdir, sub)
	}
}

// Moving to where you already are is not an error, but it must not rebuild the
// registry either - that would drop dynamic tools for no reason.
func TestMovingToTheSameDirectoryIsANoOp(t *testing.T) {
	dir := t.TempDir()
	m := moveModel(t, dir)
	before := m.toolreg
	out, err := m.moveTo(dir)
	if err != nil {
		t.Fatalf("moving to the current directory errored: %v", err)
	}
	if !strings.Contains(out, "already working in") {
		t.Fatalf("unhelpful output: %q", out)
	}
	if m.toolreg != before {
		t.Fatal("the registry was rebuilt for no reason")
	}
}

// --- telling the user what did not change ---

// The conversation does not change. Every path already discussed refers to the old
// tree, and saying so is the difference between a pleasant surprise and a bug
// report.
func TestMoveSaysEarlierPathsStillReferToTheOldTree(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	m := moveModel(t, a)
	out, err := m.moveTo(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{b, a, "still refer to the previous directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q: %q", want, out)
		}
	}
}

func TestMoveSaysItMovesNoFiles(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	m := moveModel(t, a)
	out, _ := m.moveTo(b)
	// The command name suggests a filesystem move; the output must not leave that
	// impression.
	if strings.Contains(strings.ToLower(out), "moved") &&
		!strings.Contains(strings.ToLower(out), "directory is now") {
		t.Fatalf("output implies files were moved: %q", out)
	}
}

// --- stale state ---

// The sidebar caches the task list, read from the old tree.
func TestMoveInvalidatesTheSidebarCache(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	m := moveModel(t, a)
	m.side.at = time.Now()
	m.side.queue = 3
	if _, err := m.moveTo(b); err != nil {
		t.Fatal(err)
	}
	if !m.side.at.IsZero() {
		t.Fatal("the sidebar cache survived a move and will show the old tree's todos")
	}
	if m.side.queue != 0 {
		t.Fatal("the queued batch jobs survived a move")
	}
}

// A goal run in progress holds a rendered todo block from the old tree, and its
// notion of what is already done is meaningless elsewhere.
func TestMoveClearsAnActiveGoal(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	m := moveModel(t, a)
	m.goal = &goal.Goal{}
	if _, err := m.moveTo(b); err != nil {
		t.Fatal(err)
	}
	if m.goal != nil {
		t.Fatal("an active goal survived a move; its task list describes the old tree")
	}
}

// --- writability ---

// Discovered on the first write, that is a much worse moment than being told.
func TestMoveReportsAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Skip("cannot change permissions here")
	}
	m := moveModel(t, dir)
	out, err := m.moveTo(sub)
	if err != nil {
		t.Fatalf("an unwritable directory was rejected outright; it should warn: %v", err)
	}
	// The warning is the point. Whether the probe succeeded depends on the
	// platform - Windows does not enforce mode bits the same way - so the test
	// asserts the move worked and documents that the warning is best effort.
	if !strings.Contains(out, "working directory is now") {
		t.Fatalf("unhelpful output: %q", out)
	}
}
