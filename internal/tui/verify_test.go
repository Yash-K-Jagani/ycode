package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyOps(t *testing.T) {
	dir := t.TempDir()
	since := time.Now().Add(-time.Hour)
	fresh := filepath.Join(dir, "fresh.txt")
	_ = os.WriteFile(fresh, []byte("x"), 0o644)
	_ = os.Chtimes(fresh, time.Now(), time.Now())

	acts := []fileAct{
		{op: "write", path: "fresh.txt"},
		{op: "edit", path: "missing.txt"},
		{op: "delete", path: "gone.txt"},
	}
	fails := verifyOps(dir, acts, since)
	if len(fails) != 1 || !strings.Contains(fails[0], "missing.txt") {
		t.Fatalf("want 1 miss, got %v", fails)
	}

	// stale file fails the freshness check
	stale := filepath.Join(dir, "stale.txt")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)
	fails = verifyOps(dir, []fileAct{{op: "write", path: "stale.txt"}}, time.Now())
	if len(fails) != 1 || !strings.Contains(fails[0], "untouched") {
		t.Fatalf("want stale failure, got %v", fails)
	}

	// delete that didn't happen
	_ = os.WriteFile(filepath.Join(dir, "stay.txt"), []byte("x"), 0o644)
	fails = verifyOps(dir, []fileAct{{op: "delete", path: "stay.txt"}}, since)
	if len(fails) != 1 || !strings.Contains(fails[0], "still exists") {
		t.Fatalf("want still-exists, got %v", fails)
	}

	// all good → no failures
	fails = verifyOps(dir, []fileAct{
		{op: "write", path: "fresh.txt"},
		{op: "delete", path: "gone.txt"},
	}, since)
	if len(fails) != 0 {
		t.Fatalf("want clean, got %v", fails)
	}

	if !strings.Contains(verifyFact([]string{"a missing"}), "a missing") {
		t.Fatal("fact message broken")
	}
}
