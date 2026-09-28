package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(".git"); err == nil {
		t.Skip("running inside a repo; skipping fixture")
	}
	if _, err := os.Stat(filepath.Join("..", ".git")); err == nil {
		t.Skip("running inside a repo; skipping fixture")
	}
	dir := t.TempDir()
	for _, argv := range [][]string{
		{"init"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := execIn(dir, argv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v (%s)", err, out)
		}
	}
	return dir
}

func execIn(dir string, argv ...string) *exec.Cmd {
	cmd := exec.Command("git", argv...)
	cmd.Dir = dir
	return cmd
}

func TestChangesToolRefusesToClaimCleanWhenGitFails(t *testing.T) {
	// A directory that is not a repo at all: the old code answered "clean",
	// which told the model there was nothing to do.
	dir := t.TempDir()
	tool := &ChangesTool{Workdir: dir}
	out, err := tool.Run(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected an error for a non-repo workdir, got output %q", out)
	}
	if strings.Contains(out, "clean") {
		t.Fatalf("a git failure must not be reported as clean: %q", out)
	}
	if !strings.Contains(err.Error(), "git") {
		t.Fatalf("the error should say git failed: %v", err)
	}
}

func TestChangesToolReportsRealStatus(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &ChangesTool{Workdir: dir}
	out, err := tool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("an untracked file should still report: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("status should list the untracked file:\n%s", out)
	}
	if strings.Contains(out, "clean") {
		t.Fatalf("a dirty tree must not be reported as clean:\n%s", out)
	}
}

func TestDirtyTreeDistinguishesCleanDirtyAndUnknown(t *testing.T) {
	dir := initRepo(t)
	st, err := DirtyTree(dir)
	if err != nil {
		t.Fatalf("a fresh repo is readable: %v", err)
	}
	if st != "" {
		t.Fatalf("a fresh repo is clean, got %q", st)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = DirtyTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "b.txt") {
		t.Fatalf("dirty tree = %q", st)
	}
	// Unknown: not a repo. This must be an error, not "".
	if _, err := DirtyTree(t.TempDir()); err == nil {
		t.Fatal("a non-repo must report an error, not a clean tree")
	}
}

func TestIsGitRepo(t *testing.T) {
	dir := initRepo(t)
	if !IsGitRepo(dir) {
		t.Fatal("the fixture repo should be detected")
	}
	if IsGitRepo(t.TempDir()) {
		t.Fatal("a plain directory is not a repo")
	}
}
