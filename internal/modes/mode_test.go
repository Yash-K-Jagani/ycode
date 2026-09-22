package modes

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

func TestHarnessAwareness(t *testing.T) {
	reg := tools.NewRegistry()
	dir := t.TempDir()
	reg.Add(&tools.ReadTool{})
	reg.Add(&tools.GrepTool{})
	reg.Add(&tools.GlobTool{})
	for _, m := range []Mode{Chat, Plan, Build, Thinking} {
		p := SystemPrompt(m, reg, dir)
		for _, want := range []string{"HARNESS", "ycode", "<tool_result>", "/doctor", "/models", "never invent"} {
			if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
				t.Fatalf("mode %s missing %q", m, want)
			}
		}
	}
	build := SystemPrompt(Build, reg, dir)
	if !strings.Contains(build, "WHEN TO USE EACH TOOL") {
		t.Fatal("build missing routing")
	}
	chat := SystemPrompt(Chat, reg, dir)
	if strings.Contains(chat, "WHEN TO USE EACH TOOL") {
		t.Fatal("chat should not carry routing")
	}
}
