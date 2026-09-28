package agent

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

func TestBuildSystemOrdersSections(t *testing.T) {
	dir := t.TempDir()
	reg := tools.DefaultRegistry(dir)
	p := BuildSystem(SystemOptions{
		Mode: modes.Build, Registry: reg, Workdir: dir, Model: "gpt-4o",
		Agent: "planner", Headless: true, Skill: "SKILL TEXT",
		GoalBlock: "GOALBLOCK", PendingPlan: "PLAN TEXT", CarryPlan: true,
		RagContext: "RAG TEXT", ZeroLeak: true, Extra: "EXTRA TEXT",
	})
	// Order matters: the model reads top-down, and the repo context has to
	// come after the goal/plan so it reads as background, not instruction.
	want := []string{
		"MODE: BUILD",
		"Active agent: planner",
		"HEADLESS:",
		"SKILL TEXT",
		"GOALBLOCK",
		"PLAN TEXT",
		"Repo tree",
		"RAG TEXT",
		"ZERO-DATA-LEAK",
		"EXTRA TEXT",
	}
	at := -1
	for _, w := range want {
		i := strings.Index(p, w)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", w, p)
		}
		if i < at {
			t.Fatalf("%q is out of order in:\n%s", w, p)
		}
		at = i
	}
}

// A plan only carries over when the caller says it should, which is the case
// only when the user actually asked to build it.
func TestBuildSystemOnlyCarriesAPlanWhenAsked(t *testing.T) {
	dir := t.TempDir()
	o := SystemOptions{Mode: modes.Build, Workdir: dir, PendingPlan: "PLAN TEXT"}
	if strings.Contains(BuildSystem(o), "PLAN TEXT") {
		t.Fatal("a plan must not appear unless CarryPlan is set")
	}
	o.CarryPlan = true
	if !strings.Contains(BuildSystem(o), "PLAN TEXT") {
		t.Fatal("a carried plan is missing")
	}
}

func TestBuildSystemRespectsRepoContextOverride(t *testing.T) {
	dir := t.TempDir()
	// chat mode has no repo context by default.
	if strings.Contains(BuildSystem(SystemOptions{Mode: modes.Chat, Workdir: dir}), "Repo tree") {
		t.Fatal("chat mode should not carry a repo tree")
	}
	yes := true
	if !strings.Contains(BuildSystem(SystemOptions{Mode: modes.Chat, Workdir: dir, RepoContext: &yes}), "Repo tree") {
		t.Fatal("an explicit override should add it")
	}
	no := false
	if strings.Contains(BuildSystem(SystemOptions{Mode: modes.Build, Workdir: dir, RepoContext: &no}), "Repo tree") {
		t.Fatal("an explicit override should remove it")
	}
	// Goal mode gets repo context without being asked.
	if !strings.Contains(BuildSystem(SystemOptions{Mode: modes.Goal, Workdir: dir}), "Repo tree") {
		t.Fatal("goal mode should see the repo")
	}
}

func TestBuildSystemZeroLeakText(t *testing.T) {
	dir := t.TempDir()
	p := BuildSystem(SystemOptions{Mode: modes.Build, Workdir: dir, ZeroLeak: true})
	if !strings.Contains(p, "guardrail against accidents, not a sandbox") {
		t.Fatalf("the ZDL note must be honest about bash/run:\n%s", p)
	}
	if strings.Contains(BuildSystem(SystemOptions{Mode: modes.Build, Workdir: dir}), "ZERO-DATA-LEAK") {
		t.Fatal("the ZDL note must not appear when the mode is off")
	}
}

// An unknown agent name should not lose the flavor line entirely.
func TestBuildSystemToleratesUnknownAgent(t *testing.T) {
	p := BuildSystem(SystemOptions{Mode: modes.Chat, Workdir: t.TempDir(), Agent: "wizard"})
	if !strings.Contains(p, "Active agent: wizard") {
		t.Fatalf("unknown agent was dropped:\n%s", p)
	}
}
