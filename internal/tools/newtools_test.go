package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/db"
)

func TestTree(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	out, err := (TreeTool{}).Run(context.Background(), json.RawMessage(`{"path":`+quote(dir)+`,"depth":3}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.go", "sub/", "b.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".git") {
		t.Fatalf(".git leaked:\n%s", out)
	}
}

func TestTodo(t *testing.T) {
	dir := t.TempDir()
	tl := &TodoTool{Workdir: dir}
	ctx := context.Background()
	if _, err := tl.Run(ctx, json.RawMessage(`{"action":"add","text":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.Run(ctx, json.RawMessage(`{"action":"add","text":"second"}`)); err != nil {
		t.Fatal(err)
	}
	out, _ := tl.Run(ctx, json.RawMessage(`{"action":"list"}`))
	if !strings.Contains(out, "1. first") || !strings.Contains(out, "2. second") {
		t.Fatalf("bad list:\n%s", out)
	}
	if _, err := tl.Run(ctx, json.RawMessage(`{"action":"done","id":1}`)); err != nil {
		t.Fatal(err)
	}
	out, _ = tl.Run(ctx, json.RawMessage(`{"action":"list"}`))
	if !strings.Contains(out, "[x] 1.") {
		t.Fatalf("not marked:\n%s", out)
	}
	if _, err := tl.Run(ctx, json.RawMessage(`{"action":"done","id":99}`)); err == nil {
		t.Fatal("expected missing-id error")
	}
}

func TestMemory(t *testing.T) {
	mt := &MemoryTool{Dir: t.TempDir()}
	ctx := context.Background()
	if _, err := mt.Run(ctx, json.RawMessage(`{"action":"save","key":"proj","text":"uses go"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := mt.Run(ctx, json.RawMessage(`{"action":"recall","key":"proj"}`))
	if err != nil || got != "uses go" {
		t.Fatalf("bad recall: %q %v", got, err)
	}
	if _, err := mt.Run(WithReadOnly(ctx), json.RawMessage(`{"action":"save","key":"x","text":"y"}`)); err == nil {
		t.Fatal("expected read-only block on save")
	}
	if _, err := mt.Run(WithReadOnly(ctx), json.RawMessage(`{"action":"recall","key":"proj"}`)); err != nil {
		t.Fatalf("recall should work read-only: %v", err)
	}
}

func TestMemorySQLite(t *testing.T) {
	db.ResetSharedForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(db.ResetSharedForTest)
	mt := &MemoryTool{}
	ctx := context.Background()
	if _, err := mt.Run(ctx, json.RawMessage(`{"action":"save","key":"k","text":"v"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := (&MemoryTool{}).Run(ctx, json.RawMessage(`{"action":"recall","key":"k"}`))
	if err != nil || got != "v" {
		t.Fatalf("kv recall: %q %v", got, err)
	}
}

func TestPatch(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "base")

	pt := &PatchTool{Workdir: dir}
	diff := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n"
	out, err := pt.Run(context.Background(), json.RawMessage(`{"diff":`+quote(diff)+`}`))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if !strings.Contains(string(data), "TWO") {
		t.Fatalf("patch not applied:\n%s", data)
	}
	if _, err := pt.Run(context.Background(), json.RawMessage(`{"diff":"garbage"}`)); err == nil {
		t.Fatal("expected failure on bad diff")
	}
	ro := &PatchTool{Workdir: dir}
	if _, err := ro.Run(WithReadOnly(context.Background()), json.RawMessage(`{"diff":`+quote(diff)+`}`)); err == nil {
		t.Fatal("expected read-only block")
	}
}
