package tui

import (
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
	if !strings.Contains(out, "x := 1") || !strings.Contains(out, "Here") || !strings.Contains(out, "Done") {
		t.Fatalf("content lost:\n%s", out)
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
