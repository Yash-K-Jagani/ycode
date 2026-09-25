package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestSplitFences(t *testing.T) {
	segs := splitFences("hi\n```go\nx := 1\n```\nbye")
	if len(segs) != 3 || segs[0].code || !segs[1].code || segs[2].code {
		t.Fatalf("%+v", segs)
	}
	segs = splitFences("no fences")
	if len(segs) != 1 || segs[0].code {
		t.Fatal("plain text should be one prose seg")
	}
	segs = splitFences("```\nunclosed")
	if len(segs) != 1 || !segs[0].code {
		t.Fatal("unclosed fence should be code")
	}
}

func TestRenderAssistant(t *testing.T) {
	out := renderAssistant("Here:\n```go\nx := 1\n```\nDone")
	plain := stripANSI(out)
	for _, want := range []string{"Here", "x := 1", "Done"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("content lost (%q):\n%s", want, out)
		}
	}
	if strings.Contains(out, "```") {
		t.Fatalf("fences should be consumed:\n%s", out)
	}
	out = renderAssistant(`run this <tool:bash>{"command":"go test"}</tool:bash> now`)
	if !strings.Contains(out, "<tool:bash>") {
		t.Fatalf("tool line lost:\n%s", out)
	}
	if renderAssistant("   \n  ") != "" {
		t.Fatal("blank should render empty")
	}
}

func TestIsToolLine(t *testing.T) {
	for _, l := range []string{`<tool:read>{"a":1}</tool:read>`, `  <tool_result:x>`, "tool:bash{}", "```tool:read"} {
		if !isToolLine(l) {
			t.Fatalf("miss: %q", l)
		}
	}
	if isToolLine("regular prose") {
		t.Fatal("false positive")
	}
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestHighlight(t *testing.T) {
	hl, ok := highlight("go", "package main\nfunc main() {}\n")
	if !ok {
		t.Fatal("go should highlight")
	}
	if !strings.Contains(hl, "func") || !strings.Contains(hl, "\x1b[") {
		t.Fatalf("no highlight codes:\n%q", hl)
	}
	for _, lang := range []string{"py", "js", "rust", "cs", "ps1", "yaml", "diff"} {
		if _, ok := highlight(lang, "x"); !ok {
			t.Fatalf("%s should highlight", lang)
		}
	}
	if _, ok := highlight("notalang", "x"); ok {
		t.Fatal("unknown lang should fail")
	}
	if _, ok := highlight("", "x"); ok {
		t.Fatal("empty lang should fail")
	}
	out := renderAssistant("```python\nprint('hi')\n```")
	if !strings.Contains(out, "print") || strings.Contains(out, "```") {
		t.Fatalf("bad render:\n%s", out)
	}
}

func TestWriteOpPath(t *testing.T) {
	if got := writeOpPath(`{"path": "a/b.go", "content": "x"}`); got != "a/b.go" {
		t.Fatalf("%q", got)
	}
	if writeOpPath(`{nope`) != "" {
		t.Fatal("bad json should give empty")
	}
	if writeOpPath(`{"content":"x"}`) != "" {
		t.Fatal("missing path should give empty")
	}
}

func TestRenderDiff(t *testing.T) {
	out := renderAssistant("```diff\n@@ -1,2 +1,2 @@\n- old\n+ new\n keep?\n```")
	if strings.Contains(out, "```") {
		t.Fatalf("fences should be consumed:\n%s", out)
	}
	for _, want := range []string{"- old", "+ new", "@@"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// gutter must not leak into diff blocks (hunk headers carry numbers)
	for _, ln := range strings.Split(stripANSI(out), "\n") {
		if strings.Contains(ln, "│") {
			t.Fatalf("diff should have no gutter: %q", ln)
		}
	}
}

func TestGutter(t *testing.T) {
	got := gutterize("a\nb\nc")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("%q", got)
	}
	for i, ln := range lines {
		want := fmt.Sprintf("%d │ ", i+1)
		if !strings.Contains(stripANSI(ln), want) {
			t.Fatalf("line %d missing gutter %q: %q", i, want, ln)
		}
	}
	got = gutterize("x")
	if !strings.Contains(stripANSI(got), "1 │ x") {
		t.Fatalf("single: %q", got)
	}
}

func TestExtractDiff(t *testing.T) {
	res := "wrote f (3 bytes)\n```diff\n@@ -1,1 +1,1 @@\n- a\n+ b\n```"
	if got := extractDiff(res); !strings.Contains(got, "- a") || !strings.Contains(got, "+ b") {
		t.Fatalf("%q", got)
	}
	if extractDiff("no diff here") != "" {
		t.Fatal("expected empty")
	}
	if got := extractDiff("x\n```diff\n- a"); !strings.Contains(got, "- a") {
		t.Fatalf("unclosed: %q", got)
	}
}
