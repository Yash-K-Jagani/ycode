package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("AAA\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("BBB\n"), 0o644)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	rd := &ReadTool{}
	out, err := rd.Run(context.Background(), json.RawMessage(`{"paths":["a.txt","b.txt"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "=== a.txt ===") || !strings.Contains(out, "AAA") {
		t.Fatalf("missing a:\n%s", out)
	}
	if !strings.Contains(out, "=== b.txt ===") || !strings.Contains(out, "BBB") {
		t.Fatalf("missing b:\n%s", out)
	}
	if _, err := rd.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected path-required error")
	}
	many := `{"paths":["a","b","c","d","e","f","g","h","i","j","k"]}`
	if _, err := rd.Run(context.Background(), json.RawMessage(many)); err == nil {
		t.Fatal("expected 10-path cap error")
	}
	one, err := rd.Run(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil || !strings.Contains(one, "1: AAA") {
		t.Fatalf("single-path compat: %q %v", one, err)
	}
}

func TestChanges(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	ch := &ChangesTool{Workdir: dir}
	out, err := ch.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "branch:") {
		t.Fatalf("want branch line:\n%s", out)
	}
	_ = os.WriteFile(filepath.Join(dir, "n.txt"), []byte("x"), 0o644)
	out, err = ch.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "n.txt") {
		t.Fatalf("want untracked file:\n%s", out)
	}
	if _, err := ch.Run(WithReadOnly(context.Background()), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("changes must work read-only: %v", err)
	}
}
