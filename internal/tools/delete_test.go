package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	d := &DeleteTool{Workdir: dir}
	ctx := context.Background()
	f := filepath.Join(dir, "gone.txt")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	out, err := d.Run(ctx, json.RawMessage(`{"path":"gone.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "deleted gone.txt" {
		t.Fatalf("%q", out)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("still exists")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"path":"gone.txt"}`)); err == nil {
		t.Fatal("expected not-found")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"path":""}`)); err == nil {
		t.Fatal("expected path error")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"path":"."}`)); err == nil {
		t.Fatal("expected workdir refusal")
	}
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "a.txt"), []byte("x"), 0o644)
	if _, err := d.Run(ctx, json.RawMessage(`{"path":"sub"}`)); err == nil {
		t.Fatal("expected recursive error")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"path":"sub","recursive":true}`)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "out.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	if _, err := d.Run(ctx, json.RawMessage(`{"path":`+quote(outside)+`}`)); err == nil {
		t.Fatal("expected outside-workdir refusal")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"path":`+quote(outside)+`,"force":true}`)); err != nil {
		t.Fatalf("force should allow: %v", err)
	}
	if _, err := d.Run(WithReadOnly(ctx), json.RawMessage(`{"path":"x"}`)); err == nil {
		t.Fatal("expected read-only block")
	}
}
