package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAddRemove(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "n.txt")

	cr := &CreateTool{Workdir: dir}
	out, err := cr.Run(ctx, json.RawMessage(`{"path":"n.txt","content":"one\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+ one") {
		t.Fatalf("want diff, got:\n%s", out)
	}
	if _, err := cr.Run(ctx, json.RawMessage(`{"path":"n.txt","content":"x"}`)); err == nil {
		t.Fatal("create must fail when exists")
	}

	ad := &AddTool{Workdir: dir}
	out, err = ad.Run(ctx, json.RawMessage(`{"path":"n.txt","content":"two\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+ two") {
		t.Fatalf("want appended diff:\n%s", out)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "one\ntwo\n" {
		t.Fatalf("bad append: %q", data)
	}

	rm := &RemoveTool{Workdir: dir}
	out, err = rm.Run(ctx, json.RawMessage(`{"path":"n.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "- n.txt") {
		t.Fatalf("want remove diff:\n%s", out)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("still exists")
	}
	if _, err := rm.Run(WithReadOnly(ctx), json.RawMessage(`{"path":"n.txt"}`)); err == nil {
		t.Fatal("expected read-only block")
	}
}

func TestDiffBlock(t *testing.T) {
	d := diffBlock([]string{"a", "b", "c"}, []string{"a", "B", "c", "d"}, 60)
	if !strings.Contains(d, "- b") || !strings.Contains(d, "+ B") || !strings.Contains(d, "+ d") {
		t.Fatalf("bad diff:\n%s", d)
	}
	if strings.Contains(d, " a") && strings.Contains(d, "\n  a") {
		t.Fatal("context lines should be omitted")
	}
	if got := diffBlock([]string{"x"}, []string{"x"}, 60); !strings.Contains(got, "no changes") {
		t.Fatalf("identical: %q", got)
	}
	big := make([]string, 700)
	for i := range big {
		big[i] = "l"
	}
	if got := diffBlock(big, []string{"z"}, 60); !strings.Contains(got, "large change") {
		t.Fatalf("cap: %q", got)
	}
}

func TestEditGuardAndDiff(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644)
	ed := &EditTool{Workdir: dir}
	out, err := ed.Run(ctx, json.RawMessage(`{"path":"f.txt","old_string":"beta","new_string":"BETA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "- beta") || !strings.Contains(out, "+ BETA") {
		t.Fatalf("want edit diff:\n%s", out)
	}
	whole, _ := os.ReadFile(p)
	if _, err := ed.Run(ctx, json.RawMessage(`{"path":"f.txt","old_string":`+quote(string(whole))+`,"new_string":"x"}`)); err == nil {
		t.Fatal("expected >80% guard error")
	}
}

func TestSummary(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "s.go")
	_ = os.WriteFile(p, []byte("package main\n\nfunc Alpha() {}\nfunc Beta() {}\n"), 0o644)
	sm := &SummaryTool{}
	out, err := sm.Run(ctx, json.RawMessage(`{"path":`+quote(p)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lines", "Alpha (L", "Beta (L", "head:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if _, err := sm.Run(WithReadOnly(ctx), json.RawMessage(`{"path":`+quote(p)+`}`)); err != nil {
		t.Fatalf("summary must work read-only: %v", err)
	}
}

func TestEditLineNumberPrefix(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644)
	ed := &EditTool{Workdir: dir}
	// model copies "2: beta" from read output — must still apply
	out, err := ed.Run(ctx, json.RawMessage(`{"path":"f.txt","old_string":"2: beta","new_string":"BETA"}`))
	if err != nil {
		t.Fatalf("numbered anchor failed: %v", err)
	}
	if !strings.Contains(out, "edited") {
		t.Fatalf("%q", out)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("bad apply: %q", data)
	}
}

func TestEditMissSuggests(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("const timeoutMs = 5000\nconst retries = 3\n"), 0o644)
	ed := &EditTool{Workdir: dir}
	_, err := ed.Run(ctx, json.RawMessage(`{"path":"f.txt","old_string":"timeoutMs = 9000","new_string":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "Did you mean") {
		t.Fatalf("want suggestions, got %v", err)
	}
	if !strings.Contains(err.Error(), "L1:") {
		t.Fatalf("want line refs: %v", err)
	}
}
