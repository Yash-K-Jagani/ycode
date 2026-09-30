package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// DelegateFunc runs one subagent question and returns its summary.
//
// It is declared here, in the package that owns the tool, and implemented in
// internal/subagent. That split is what keeps the tool registered in the catalog
// - so a mode can allow-list it and the consistency check can see it exists -
// without internal/tools importing the agent loop, which would be a cycle
// through modes.
//
// A function rather than an interface because there is one implementation and it
// needs a dozen parameters (provider, mode, workdir, tracer, progress sink). An
// interface here would be a struct with those fields.
type DelegateFunc func(ctx context.Context, prompt, agent string) (string, error)

// TaskTool delegates a read-only question to a subagent.
//
// Its shape is the point: a question in, a summary out. The parent's context gets
// one result line instead of a hundred grep matches, which is what makes asking a
// broad question affordable.
//
// It carries no allow-list of its own and needs none. What a subagent may touch
// is decided from the Isolatable set, not here - see internal/subagent.Allow.
type TaskTool struct {
	// Delegate does the work. Nil means nobody wired it up - a registry with no
	// provider behind it - and the call says so rather than appearing to work.
	Delegate DelegateFunc
}

func (TaskTool) Name() string { return "task" }

func (TaskTool) Description() string {
	return "Delegate a read-only question to a subagent with its own context window, " +
		"and get back a short summary instead of raw search results. Use it for broad " +
		"questions whose answer would otherwise flood the conversation: where is this " +
		"called, what does this module do, find every usage of X. " +
		"Agents: explorer, analyst. " +
		"Args: prompt (required), agent (optional). " +
		"The subagent can only read - it cannot edit, write or run commands."
}

func (TaskTool) Schema() string {
	return `{"type":"object","required":["prompt"],"properties":{` +
		`"prompt":{"type":"string"},` +
		`"agent":{"type":"string","enum":["explorer","analyst"]}}}`
}

func (t *TaskTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Delegate == nil {
		// Said plainly. A tool that exists in the catalog but was never wired is a
		// bug, and the model should see that rather than a tool call that quietly
		// returns nothing.
		return "", fmt.Errorf("task is registered but not available in this context")
	}
	var a struct {
		Prompt string `json:"prompt"`
		Agent  string `json:"agent"`
	}
	if len(args) == 0 {
		return "", fmt.Errorf("missing args")
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad args for task: must be a valid JSON object (%v). Schema: %s", err, t.Schema())
	}
	if a.Prompt == "" {
		return "", fmt.Errorf("provide prompt (the question for the subagent)")
	}
	return t.Delegate(ctx, a.Prompt, a.Agent)
}
