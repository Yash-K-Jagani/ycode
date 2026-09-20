package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	if isWindows() {
		_ = dir
	}
	return DefaultRegistry(dir), dir
}

func TestReadWriteEdit(t *testing.T) {
	r, dir := testRegistry(t)
	ctx := context.Background()
	p := filepath.Join(dir, "sub", "f.txt")

	wr, _ := r.Get("write")
	out, err := wr.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`,"content":"hello\nworld\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(out)

	rd, _ := r.Get("read")
	got, err := rd.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1: hello") || !strings.Contains(got, "2: world") {
		t.Fatalf("bad read output:\n%s", got)
	}

	ed, _ := r.Get("edit")
	if _, err := ed.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`,"old_string":"world","new_string":"gopher"}`)); err != nil {
		t.Fatal(err)
	}
	got2, _ := rd.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`}`))
	if !strings.Contains(got2, "gopher") {
		t.Fatalf("edit did not apply:\n%s", got2)
	}
	if _, err := ed.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`,"old_string":"missing","new_string":"x"}`)); err == nil {
		t.Fatal("expected error for missing old_string")
	}
}

func TestGrepGlob(t *testing.T) {
	r, dir := testRegistry(t)
	ctx := context.Background()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n// TODO fix\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing\n"), 0o644)

	grep, _ := r.Get("grep")
	out, err := grep.Run(ctx, json.RawMessage(`{"pattern":"TODO","path":`+quote(dir)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go:2") {
		t.Fatalf("bad grep output:\n%s", out)
	}

	glob, _ := r.Get("glob")
	out2, err := glob.Run(ctx, json.RawMessage(`{"pattern":"*.go","path":`+quote(dir)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "a.go") {
		t.Fatalf("bad glob output:\n%s", out2)
	}
}

func TestBash(t *testing.T) {
	r, _ := testRegistry(t)
	ctx := context.Background()
	bash, _ := r.Get("bash")
	out, err := bash.Run(ctx, json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("bad bash output: %q", out)
	}
	if _, err := bash.Run(ctx, json.RawMessage(`{"command":"rm -rf / tmp"}`)); err == nil {
		t.Fatal("expected denylist block")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
