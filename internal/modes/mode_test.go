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
		p := SystemPrompt(m, reg, dir, "big-model")
		for _, want := range []string{"ENVIRONMENT", "ycode", "<tool_result>", "/doctor", "/models", "never invent", "not the harness"} {
			if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
				t.Fatalf("mode %s missing %q", m, want)
			}
		}
		if strings.Contains(p, "You are ycode,") {
			t.Fatalf("mode %s must not identify as ycode itself", m)
		}
	}
	build := SystemPrompt(Build, reg, dir, "big-model")
	if !strings.Contains(build, "WHEN TO USE EACH TOOL") {
		t.Fatal("build missing routing")
	}
	chat := SystemPrompt(Chat, reg, dir, "big-model")
	if strings.Contains(chat, "WHEN TO USE EACH TOOL") {
		t.Fatal("chat should not carry routing")
	}
}

func TestCompactSmallModel(t *testing.T) {
	reg := tools.NewRegistry()
	dir := t.TempDir()
	reg.Add(&tools.ReadTool{})
	full := SystemPrompt(Build, reg, dir, "qwen2.5-coder:7b")
	small := SystemPrompt(Build, reg, dir, "qwen2.5-coder:1.5b")
	if !strings.Contains(full, "\n  schema:") {
		t.Fatal("full prompt should include schemas")
	}
	if strings.Contains(small, "\n  schema:") {
		t.Fatal("compact prompt must drop schemas")
	}
	if strings.Contains(small, "WHEN TO USE EACH TOOL") {
		t.Fatal("compact prompt must drop full routing")
	}
	if !strings.Contains(small, "TOOLS:") || !strings.Contains(small, "read") {
		t.Fatal("compact prompt must keep tool list")
	}
	if len(small) >= len(full) {
		t.Fatal("compact prompt should be shorter")
	}
	for _, name := range []string{"phi3:mini", "llama3.2:1b", "tiny-model"} {
		if !isSmallModel(name) {
			t.Fatalf("%s should be small", name)
		}
	}
	if isSmallModel("qwen2.5-coder:32b") || isSmallModel("gpt-4o") {
		t.Fatal("big models must not match")
	}
}

func TestPromptStaysLean(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.ReadTool{})
	full := SystemPrompt(Build, reg, t.TempDir(), "big-model")
	small := SystemPrompt(Build, reg, t.TempDir(), "tiny-1b")
	if len(small) >= len(full) {
		t.Fatal("compact must be shorter")
	}
	// tripwire against prompt bloat: full build prompt stays under ~12KB
	if len(full) > 12*1024 {
		t.Fatalf("prompt bloated: %d bytes", len(full))
	}
	for _, want := range []string{"TOOLS", "ENVIRONMENT", "never fenced", "Max 8 rounds"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
