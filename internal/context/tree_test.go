package ctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTree(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "a.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".git", "y"), []byte("x"), 0o644)

	out := Tree(dir, 150, 4000)
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "sub/a.go") {
		t.Fatalf("missing entries:\n%s", out)
	}
	if strings.Contains(out, ".git") {
		t.Fatalf(".git should be skipped:\n%s", out)
	}
	if got := Tree(dir, 1, 4000); strings.Count(got, "\n") != 1 {
		t.Fatalf("entry cap failed:\n%s", got)
	}
	if got := Tree(dir, 150, 8); !strings.Contains(got, "truncated") {
		t.Fatalf("char cap failed:\n%s", got)
	}
	if Tree(filepath.Join(dir, "missing"), 10, 100) != "" {
		t.Fatal("missing dir should give empty tree")
	}
}
