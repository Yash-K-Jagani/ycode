package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchDirs(t *testing.T) {
	mk := t.TempDir()
	_ = os.MkdirAll(filepath.Join(mk, "sub"), 0o755)
	_ = os.MkdirAll(filepath.Join(mk, ".git"), 0o755)
	if got := watchDirs(mk); len(got) != 2 {
		t.Fatalf("want root+sub, got %v", got)
	}
}

func TestWatchStartStop(t *testing.T) {
	m := &Model{workdir: t.TempDir()}
	if m.watching() {
		t.Fatal("should start off")
	}
	// target with no test setup: initial run fails gracefully in background
	m.startWatch(m.workdir)
	if !m.watching() {
		t.Fatal("should be watching")
	}
	time.Sleep(300 * time.Millisecond)
	m.stopWatch()
	if m.watching() {
		t.Fatal("should have stopped")
	}
	// restart keeps watching (old loop cancelled, new one live)
	m.startWatch(m.workdir)
	m.startWatch(m.workdir)
	if !m.watching() {
		t.Fatal("restart should stay watching")
	}
	m.stopWatch()
}
