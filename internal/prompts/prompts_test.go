package prompts

import (
	"strings"
	"testing"
)

func TestLibrary(t *testing.T) {
	m := NewManagerAt(t.TempDir())
	if len(m.List()) != 4 {
		t.Fatalf("expected 4 builtins, got %v", m.List())
	}
	body, err := m.Show("review")
	if err != nil || !strings.Contains(body, "{{input}}") {
		t.Fatalf("bad builtin: %v", body)
	}
	if out := Render(body, "DIFF"); !strings.Contains(out, "DIFF") || strings.Contains(out, "{{input}}") {
		t.Fatalf("bad render: %q", out)
	}
	if err := m.Save("custom", "hello {{input}}"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save("custom", "hello v2 {{input}}"); err != nil {
		t.Fatal(err)
	}
	if vs := m.Versions("custom"); len(vs) != 1 {
		t.Fatalf("expected 1 snapshot, got %v", vs)
	}
	if _, err := m.Show("missing"); err == nil {
		t.Fatal("expected not-found")
	}
}
