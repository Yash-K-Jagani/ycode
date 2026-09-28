package agent

import (
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/agents"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// SystemOptions is the per-turn variation when building a system prompt.
//
// The prompt used to be assembled at three call sites — the TUI turn, the
// headless turn and the headless goal run — and the repo context block was
// byte-identical in all three. That is how the paths drifted, why a fix had to
// be made three times, and why headless kept missing things the TUI had (a
// goal block, webhooks, a rejection note). One builder, called by all of them,
// so "what a mode's prompt contains" is a fact about the mode rather than
// something each caller remembers.
type SystemOptions struct {
	Mode     modes.Mode
	Registry *tools.Registry
	Workdir  string
	Model    string

	// Agent is the active flavor; empty leaves it out.
	Agent string
	// Headless adds the "no interactive user" line.
	Headless bool
	// RepoContext forces the repo tree/brief on or off; nil follows the mode.
	RepoContext *bool
	// Skill is instruction text from /skills run or /prompts run.
	Skill string
	// GoalBlock is the ACTIVE GOAL section for goal mode.
	GoalBlock string
	// PendingPlan is an approved plan to carry into this turn. It is only
	// included when CarryPlan is set, because a plan only carries over when
	// the user actually asked for it ("build it").
	PendingPlan string
	CarryPlan   bool
	// RagContext is pre-retrieved RAG context, if any.
	RagContext string
	// ZeroLeak appends the zero-data-leak note.
	ZeroLeak bool
	// Extra is appended verbatim, last.
	Extra string
}

// Budgets for the repo tree, so every caller shows the model the same thing.
const (
	RepoTreeEntries = 150
	RepoTreeChars   = 4000
)

// BuildSystem assembles the system prompt for one turn.
func BuildSystem(o SystemOptions) string {
	var b strings.Builder
	b.WriteString(modes.SystemPrompt(o.Mode, o.Registry, o.Workdir, o.Model))

	if o.Agent != "" {
		if ag, ok := agents.Get(o.Agent); ok {
			b.WriteString("\nActive agent: " + ag.Name + " — " + ag.Prompt)
		} else {
			b.WriteString("\nActive agent: " + o.Agent)
		}
	}
	if o.Headless {
		b.WriteString("\nHEADLESS: no interactive user. Do the task, verify with tools, output the final result.")
	}
	if o.Skill != "" {
		b.WriteString("\nActive skill instructions:\n" + o.Skill)
	}
	if o.GoalBlock != "" {
		b.WriteString(o.GoalBlock)
	}
	if o.PendingPlan != "" && o.CarryPlan {
		b.WriteString("\nApproved plan from the earlier planning turn (user said build it) — implement it step by step with tools, in order:\n" + o.PendingPlan)
	}
	if o.wantsRepoContext() {
		b.WriteString(RepoContext(o.Workdir))
	}
	if o.RagContext != "" {
		b.WriteString("\n" + o.RagContext)
	}
	if o.ZeroLeak {
		b.WriteString("\nZERO-DATA-LEAK: local only. Cloud providers are blocked, and so is every tool that reaches the network " +
			"(api, browser, github, git, db, notebook, scaffold, mcp__*, plugin__*). " +
			"bash and run still execute arbitrary commands, so treat this as a guardrail against accidents, not a sandbox.")
	}
	if o.Extra != "" {
		b.WriteString(o.Extra)
	}
	return b.String()
}

func (o SystemOptions) wantsRepoContext() bool {
	if o.RepoContext != nil {
		return *o.RepoContext
	}
	return modes.UsesRepoContext(o.Mode)
}

// RepoContext is the file tree, the codebase brief and the "never ask for
// paths" rule, identical for every caller — which is the whole point of
// having one.
func RepoContext(workdir string) string {
	var b strings.Builder
	b.WriteString("\nRepo tree (" + workdir + ") — real paths, use them directly:\n" +
		yctx.Tree(workdir, RepoTreeEntries, RepoTreeChars) +
		"NEVER ask the user for paths or locations. If a file is named without a path, find it with glob/grep yourself.")
	if brief := yctx.Brief(workdir); brief != "" {
		b.WriteString("\nCodebase brief (what this repo is, its rules, git state):\n" + brief)
	}
	return b.String()
}
