package ctx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrief(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Demo\nDoes things.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Be terse.\n"), 0o644)
	got := Brief(dir)
	if !strings.Contains(got, "Does things.") || !strings.Contains(got, "Be terse.") {
		t.Fatalf("missing content:\n%s", got)
	}
	if strings.Contains(got, "GIT:") {
		t.Fatal("no repo — no git section expected")
	}
	// empty dir → empty brief
	if Brief(t.TempDir()) != "" {
		t.Fatal("expected empty brief")
	}
	// git repo → branch + status
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("no git: %s", out)
	}
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644)
	got = Brief(dir)
	if !strings.Contains(got, "branch ") || !strings.Contains(got, "new.txt") {
		t.Fatalf("bad git section:\n%s", got)
	}
}
