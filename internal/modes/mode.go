package modes

import (
	"fmt"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/lang"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

type Mode string

const (
	Chat     Mode = "chat"
	Plan     Mode = "plan"
	Build    Mode = "build"
	Thinking Mode = "thinking"
)

func Parse(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case Chat, Plan, Build, Thinking:
		return Mode(strings.ToLower(strings.TrimSpace(s))), nil
	default:
		return "", fmt.Errorf("unknown mode %q (chat/plan/build/thinking)", s)
	}
}

func Order() []Mode { return []Mode{Plan, Build, Chat, Thinking} }

func AllowedTools(m Mode, extra ...string) []string {
	var base []string
	switch m {
	case Build:
		base = []string{"read", "write", "edit", "grep", "glob", "bash", "git", "github", "browser", "testgen", "security", "tree", "todo", "memory", "patch", "run"}
	case Plan:
		base = []string{"read", "grep", "glob", "git", "browser", "security", "tree", "todo"}
	default:
		base = nil
	}
	return append(base, extra...)
}

func SystemPrompt(m Mode, reg *tools.Registry, workdir string) string {
	base := "You are ycode, a terminal AI coding harness. Workdir: " + workdir + ". Be concise."
	if proj, ok := lang.Detect(workdir); ok {
		base += " Project language: " + proj.Language + "."
	}
	switch m {
	case Plan:
		return base + "\nMODE: PLAN (read-only). Research with read/grep/glob/git and answer or plan. If you propose file changes, output a numbered step-by-step plan and end with 'AWAITING APPROVAL'; for plain questions just answer directly. Do NOT write or edit files. For history use the git tool (log/diff/show/status work in plan mode) — bash is disabled here. Stay on task: don't repeat tool calls that already returned." + toolDocs(reg, AllowedTools(m))
	case Build:
		return base + "\nMODE: BUILD. Use tools to read, write, edit and verify code. After edits, re-read or run tests when sensible." + routing() + toolDocs(reg, AllowedTools(m))
	case Thinking:
		return base + "\nMODE: THINKING. Think step by step inside <scratchpad>...</scratchpad> (visible), then give the final answer. No tools in this mode — reason from conversation history."
	default:
		return base + "\nMODE: CHAT. Plain conversation, no tools."
	}
}

func routing() string {
	return "\nWHEN TO USE EACH TOOL (pick the right one, don't default to git/bash):" +
		"\n- find/list files: glob. read file contents: read. search code: grep." +
		"\n- user gives a URL or asks about a webpage: browser (fetch it yourself, never ask the user to paste it)." +
		"\n- run project tests: testgen. check for leaked secrets: security." +
		"\n- GitHub repos/PRs/issues or clone a link: github. repo history/diffs/commits: git." +
		"\n- user gives a repo/skill/plugin link (or owner/repo): download it with github clone, skills install, or plugins install — never ask them to do it manually." +
		"\n- map directories: tree. multi-step work: track it with todo. remember user facts: memory. apply a unified diff: patch." +
		"\n- run code and show output: run tool (inline code or a file, 10 languages). run tests: testgen (use run filter for one test)." +
		"\n- to ADD tests: write the test file first (e.g. *_test.go), then verify with testgen." +
		"\n- BIG task? FIRST break it into small steps with todo add (one per step, keep each tiny), work them in order, mark each done. The user watches this list live." +
		"\n- in PLAN mode bash/write/edit are disabled: use the git tool (log/diff/show/status all work) and read/grep/glob." +
		"\n- run shell commands only via bash (project dir, destructive cmds are blocked)." +
		"\n- stay on task: call only the tools needed for THIS request. Don't explore the repo for fun." +
		"\n- a tool error shows its schema: fix your args and retry, don't abandon the task.\n"
}

func toolDocs(reg *tools.Registry, names []string) string {
	if len(names) == 0 || reg == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nTOOLS — to act, emit a line EXACTLY like this (raw text, never in code fences, args = valid JSON object):\n<tool:NAME>{\"arg\": \"value\"}</tool:NAME>\nExample: <tool:read>{\"path\": \"main.go\"}</tool:read>\nWait for each result before the next call. Max 8 rounds.\n")
	for _, n := range names {
		t, ok := reg.Get(n)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n  schema: %s\n", t.Name(), t.Description(), t.Schema())
	}
	return b.String()
}
