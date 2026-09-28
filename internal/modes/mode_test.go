package modes

import (
	"strconv"
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
	for _, m := range []Mode{Chat, Plan, Build, Goal, Thinking} {
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

func TestBuildActsFirst(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.ReadTool{})
	for _, model := range []string{"big-model", "tiny-1b"} {
		p := SystemPrompt(Build, reg, t.TempDir(), model)
		for _, want := range []string{"START BUILDING IMMEDIATELY", "no preamble"} {
			if !strings.Contains(p, want) {
				t.Fatalf("model %s missing %q", model, want)
			}
		}
	}
	small := SystemPrompt(Build, reg, t.TempDir(), "tiny-1b")
	if !strings.Contains(small, "act first") {
		t.Fatal("compact routing should keep act-first")
	}
}

func TestParseAcceptsGoal(t *testing.T) {
	for _, in := range []string{"goal", "GOAL", " goal "} {
		m, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if m != Goal {
			t.Fatalf("Parse(%q) = %q, want goal", in, m)
		}
	}
	if _, err := Parse("nope"); err == nil {
		t.Fatal("unknown mode should error")
	} else if !strings.Contains(err.Error(), "goal") {
		t.Fatalf("error should list goal: %v", err)
	}
}

func TestOrderCyclesGoal(t *testing.T) {
	order := Order()
	if len(order) != 5 {
		t.Fatalf("Order() = %v, want 5 modes", order)
	}
	found := -1
	for i, m := range order {
		if m == Goal {
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("Order() missing goal: %v", order)
	}
	if found != 1 {
		t.Fatalf("goal should sit right after plan, got index %d in %v", found, order)
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] == order[i] {
			t.Fatalf("duplicate mode in Order(): %v", order)
		}
	}
}

func TestRounds(t *testing.T) {
	if Rounds(Goal) <= Rounds(Build) {
		t.Fatalf("goal rounds %d must exceed build %d", Rounds(Goal), Rounds(Build))
	}
	if Rounds(Plan) > Rounds(Build) {
		t.Fatal("plan should not exceed build")
	}
	if Rounds(Chat) != 0 || Rounds(Thinking) != 0 {
		t.Fatal("tool-less modes need no round budget")
	}
	for _, m := range []Mode{Plan, Build, Goal} {
		p := SystemPrompt(m, tools.NewRegistry(), t.TempDir(), "big-model")
		if !strings.Contains(p, "Max "+strconv.Itoa(Rounds(m))+" rounds") {
			t.Fatalf("mode %s prompt should state its %d-round budget", m, Rounds(m))
		}
	}
}

func TestGoalAllowedTools(t *testing.T) {
	goal := AllowedTools(Goal)
	build := AllowedTools(Build)
	if len(goal) == 0 {
		t.Fatal("goal mode must have tools")
	}
	for _, n := range []string{"read", "write", "edit", "bash", "testgen", "todo", "changes"} {
		if !contains(goal, n) {
			t.Fatalf("goal missing %s", n)
		}
	}
	// delete is the one destructive tool an unattended run must not reach.
	if contains(goal, "delete") {
		t.Fatal("goal mode must not allow delete")
	}
	if !contains(build, "delete") {
		t.Fatal("build must still allow delete")
	}
	if len(AllowedTools(Goal, "mcp__srv__do")) != len(goal)+1 {
		t.Fatal("extras should be appended in goal mode")
	}
}

func TestUsesRepoContext(t *testing.T) {
	for _, m := range []Mode{Plan, Build, Goal} {
		if !UsesRepoContext(m) {
			t.Fatalf("%s should see the repo context", m)
		}
	}
	for _, m := range []Mode{Chat, Thinking} {
		if UsesRepoContext(m) {
			t.Fatalf("%s should not see the repo context", m)
		}
	}
}

func TestGoalPromptContract(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.ReadTool{})
	for _, model := range []string{"big-model", "tiny-1b"} {
		p := SystemPrompt(Goal, reg, t.TempDir(), model)
		for _, want := range []string{
			"MODE: GOAL", "UNATTENDED", "your first output is a tool call",
			"the only form that does anything is the tagged call",
			"Never write a tool result yourself", "<tool:todo>",
			"GOAL MET", "GOAL BLOCKED", "There is no third option",
			"end a turn to announce progress", "GOAL MET is checked, not believed",
			"Stay inside the goal",
		} {
			if !strings.Contains(p, want) {
				t.Fatalf("model %s goal prompt missing %q", model, want)
			}
		}
		// The model copies whatever tool syntax the prompt shows it, so an
		// untagged shorthand in goal mode means untagged calls at runtime.
		for _, shorthand := range []string{"todo add (", "todo done", "with todo add"} {
			if strings.Contains(p, shorthand) {
				t.Fatalf("goal prompt must not teach the shorthand %q", shorthand)
			}
		}
		if strings.Contains(p, "CONTINUE:") {
			t.Fatal("goal prompt must not offer a continue marker — it taught the model to narrate instead of acting")
		}
		if !strings.Contains(p, "todo") {
			t.Fatal("goal prompt must reference the todo tool")
		}
	}
	// The stop contract is the whole loop's control signal: without it the
	// harness would burn the budget without ever stopping.
	small := SystemPrompt(Goal, reg, t.TempDir(), "tiny-1b")
	if len(small) >= len(SystemPrompt(Goal, reg, t.TempDir(), "big-model")) {
		t.Fatal("compact goal prompt should be shorter")
	}
}

func TestGoalPromptStaysLean(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.ReadTool{})
	reg.Add(&tools.GrepTool{})
	p := SystemPrompt(Goal, reg, t.TempDir(), "big-model")
	if len(p) > 14*1024 {
		t.Fatalf("goal prompt bloated: %d bytes", len(p))
	}
	if strings.Contains(p, "delete") {
		t.Fatal("goal prompt must not advertise the delete tool")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
