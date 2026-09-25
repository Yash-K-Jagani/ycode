package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileLifecycle is the Phase A reliability proof: sequential create → edit → read → remove → undo
func TestFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(orig) }()
	ctx := context.Background()

	// 1. create
	cr := &CreateTool{Workdir: dir}
	if _, err := cr.Run(ctx, json.RawMessage(`{"path":"app.txt","content":"version: 1\nmode: dev\n"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 2. edit (surgical, with diffBlock)
	ed := &EditTool{Workdir: dir}
	out, err := ed.Run(ctx, json.RawMessage(`{"path":"app.txt","old_string":"mode: dev","new_string":"mode: prod"}`))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(out, "- mode: dev") || !strings.Contains(out, "+ mode: prod") {
		t.Fatalf("edit diff missing:\n%s", out)
	}

	// 3. read (backwards compat: single path or paths array)
	rd := &ReadTool{}
	got, err := rd.Run(ctx, json.RawMessage(`{"path":"app.txt"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(got, "mode: prod") {
		t.Fatalf("read after edit:\n%s", got)
	}
	// multi-path read
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B\n"), 0644)
	multi, err := rd.Run(ctx, json.RawMessage(`{"paths":["app.txt","b.txt"]}`))
	if err != nil || !strings.Contains(multi, "=== app.txt ===") || !strings.Contains(multi, "=== b.txt ===") {
		t.Fatalf("multi read: %v\n%s", err, multi)
	}

	// 4. remove -> verify gone, undo -> verify restored
	rm := &RemoveTool{Workdir: dir}
	if _, err := rm.Run(ctx, json.RawMessage(`{"path":"app.txt"}`)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.txt")); !os.IsNotExist(err) {
		t.Fatal("still exists after remove")
	}
	restored, err := UndoLast(dir)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if restored == "" {
		t.Fatal("undo returned empty path")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "app.txt"))
	if !strings.Contains(string(data), "mode: prod") {
		t.Fatalf("undo content wrong: %q", string(data))
	}

	// 5. delete sibling tool still works
	_ = os.WriteFile(filepath.Join(dir, "tmp.txt"), []byte("x\n"), 0644)
	dl := &DeleteTool{Workdir: dir}
	if _, err := dl.Run(ctx, json.RawMessage(`{"path":"tmp.txt"}`)); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
