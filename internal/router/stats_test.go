package router

import (
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/db"
)

func isolate(t *testing.T) {
	t.Helper()
	db.ResetSharedForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(db.ResetSharedForTest)
}

func TestStatsSQLiteCarryOver(t *testing.T) {
	isolate(t)
	s := NewStats()
	s.Record("ollama", time.Second, 100, true)
	s2 := NewStats()
	if !strings.Contains(s2.Summary(), "ollama") {
		t.Fatalf("not persisted:\n%s", s2.Summary())
	}
}
