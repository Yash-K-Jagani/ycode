package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndFire(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hooks.yaml")
	cmd := "echo hi-$YCODE_EVENT"
	if isWin() {
		cmd = "echo hi-$env:YCODE_EVENT"
	}
	_ = os.WriteFile(f, []byte("on_response:\n  - command: \""+cmd+"\"\n"), 0o644)
	h := LoadFiles([]string{f, filepath.Join(dir, "missing.yaml")})
	out := h.Fire(context.Background(), OnResponse, map[string]string{"mode": "chat"})
	if out == "" || !contains(out, "hi-on_response") {
		t.Fatalf("bad hook output: %q", out)
	}
	// empty hooks = no-op
	h2 := LoadFiles([]string{filepath.Join(dir, "missing.yaml")})
	if h2.Fire(context.Background(), OnError, nil) != "" {
		t.Fatal("expected empty fire")
	}
}

func TestGateBlocks(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hooks.yaml")
	var block string
	if isWin() {
		block = "pre_tool:\n  - command: \"exit 1\"\n"
	} else {
		block = "pre_tool:\n  - command: \"exit 1\"\n"
	}
	_ = os.WriteFile(f, []byte(block), 0o644)
	h := LoadFiles([]string{f})
	if err := h.Gate(context.Background(), "write", "", nil); err == nil {
		t.Fatal("expected gate to block")
	}
	h2 := LoadFiles(nil)
	if err := h2.Gate(context.Background(), "write", "", nil); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
