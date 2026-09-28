package modes

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/lang"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

type Mode string

const (
	Chat     Mode = "chat"
	Plan     Mode = "plan"
	Build    Mode = "build"
	Goal     Mode = "goal"
	Thinking Mode = "thinking"
)

// The mode registry — Parse, Order, Rounds, AllowedTools, SystemPrompt's mode block —
// lives in spec.go. This file holds the shared prompt base and the tool-doc helpers.

// SystemPrompt assembles the base prompt (who the model is, the harness it
// runs inside) and hands the mode its own block from the spec table.
func SystemPrompt(m Mode, reg *tools.Registry, workdir, model string) string {
	s := lookup(m)
	base := "You are a capable AI coding assistant running inside ycode, a terminal coding harness. Workdir: " + workdir + ". Be concise."
	if proj, ok := lang.Detect(workdir); ok {
		base += " Project language: " + proj.Language + "."
	}
	return base + harness(s) + s.prompt(s, reg, model, s.rounds)
}

// isSmallModel matches tiny models that drown in long tool schemas.
var smallModelRe = regexp.MustCompile(`(?i)(1\.5b|0\.5b|\b1b\b|mini|nano|tiny|small)`)

func isSmallModel(model string) bool { return smallModelRe.MatchString(model) }

// toolDocsCompact lists tools as one-liners (names + first sentence only).
func toolDocsCompact(reg *tools.Registry, names []string, rounds int) string {
	if len(names) == 0 || reg == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nTOOLS — act by emitting exactly: <tool:NAME>{\"arg\": \"value\"}</tool:NAME> (raw text, never fenced). Example: <tool:read>{\"path\": \"main.go\"}</tool:read>. Wait for results. Max %d rounds.\n", rounds)
	for _, n := range names {
		t, ok := reg.Get(n)
		if !ok {
			continue
		}
		desc := t.Description()
		if i := strings.IndexByte(desc, '.'); i >= 0 {
			desc = desc[:i]
		}
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), desc)
	}
	return b.String()
}

// harness describes the environment every mode shares. The Tab cycle and the
// mode list are derived from the spec table, so they cannot fall out of step
// with the registry.
func harness(s spec) string {
	cycle := make([]string, 0, len(specs))
	for _, m := range specs {
		cycle = append(cycle, string(m.mode))
	}
	modeList := strings.Join(cycle, "/")
	return "\nENVIRONMENT (where you run — you are the model, not the harness):" + "\n- You are an individual AI assistant. ycode is the harness around you: the user chats in its terminal UI and it executes your <tool:> calls as real tools on their machine, in the workdir above. Results return as <tool_result> blocks; use them, don't re-ask for what they contain." +
		"\n- Conversation persists across turns, but old history may be compacted under token pressure — re-read files instead of assuming earlier details." +
		"\n- Tool errors name the expected schema: fix args and retry. Repeating an identical call returns its cached result and ends your turn, so say the answer instead." +
		"\n- <tool_result> blocks come from the harness, never from you: writing one executes nothing — only <tool:> calls act." +
		"\n- The user drives ycode itself with slash commands and keys you should know: Tab cycles " + modeList + ", " + s.slash + " selects this mode, /goal <text> hands you a goal to work toward unattended, /models switches models (Ctrl+O cycles local ones), /help lists commands, /doctor diagnoses setup, /connect adds providers, /sessions resumes work, @file attaches files." +
		"\n- If the user seems stuck with setup, models, or keys, point them at /doctor or /connect instead of guessing. Never invent API keys, DSNs, paths, or URLs — ask or discover them with tools." +
		"\n- If asked what tools or abilities you have, answer from your tool list below: compact one-line bullets, no schemas, no repetition, then stop." +
		"\n- Never claim to be ycode itself, its developer, or its UI. If asked who you are, say you are an AI assistant running inside the ycode harness."
}

// shortRouting is the compact tool guide for small models.
func shortRouting() string {
	return "\nTOOLS: find files glob, read files read, search grep, write/edit files to change them, run tests testgen, shell bash, git history git." +
		"\nRULES: START BUILDING IMMEDIATELY — act first with tools (no questions, no preamble), never paste file content (use write/edit), never bare shell commands (use tool calls), never write result blocks, fix bad args and retry.\n"
}
func routing() string { return joinRouting(routingLines(), true) }

// routingFor is the tool guide with the delete lines dropped for modes that
// don't expose the delete tool (goal mode runs unattended). It also rewrites
// the shorthand tool references ("todo add") into prose: a small model will
// copy an instruction's syntax verbatim instead of the required tagged form,
// which is how goal mode ended up with untagged "todo add" lines that executed
// nothing.
func routingFor(hasDelete bool) string {
	if hasDelete {
		return routing()
	}
	return stripShorthand(joinRouting(routingLines(), false))
}

func stripShorthand(s string) string {
	s = strings.ReplaceAll(s, "track it with todo", "track it with the todo tool")
	s = strings.ReplaceAll(s, "with todo add (one per step, keep each tiny)", "with the todo tool, one small call per step")
	return s
}

func routingLines() []string {
	return []string{
		"\nWHEN TO USE EACH TOOL (pick the right one, don't default to git/bash):",
		"- find/list files: glob. read file contents: read (paths array reads several at once). search code: grep. what changed: changes.",
		"- new files: create (fails if exists). append: add. change part of a file: edit with a small unique old_string (never rewrite whole files with write). remove files: remove.",
		"- You CAN build complete multi-file projects yourself: create each file with its own tool call, one per line, then verify with testgen. Never refuse a build for capability reasons.",
		"- Multi-file work: one tool line per file (read takes a paths array). Check changes when done.",
		"- user gives a URL or asks about a webpage: browser (fetch it yourself, never ask the user to paste it).",
		"- run project tests: testgen. check for leaked secrets: security.",
		"- GitHub repos/PRs/issues or clone a link: github. repo history/diffs/commits: git.",
		"- user gives a repo/skill/plugin link (or owner/repo): download it with github clone, skills install, or plugins install — never ask them to do it manually.",
		"- map directories: tree. multi-step work: track it with todo. remember user facts: memory. apply a unified diff: patch.",
		"- run code and show output: run tool (inline code or a file, 10 languages). run tests: testgen (use run filter for one test).",
		"- to ADD tests: write the test file first (e.g. *_test.go), then verify with testgen.",
		"- delete files only with the delete tool (never bash rm); remove dirs need recursive:true.",
		"- NEVER paste file content or bare shell commands (rm/touch/...) as your answer — ALWAYS emit the tool call that does it. Pasting/typing instead of acting is a failure.",
		"- NEVER tell the user to do it themselves when you have the tools: DO the task with tools first, explain after.",
		"- BIG task? FIRST break it into small steps with todo add (one per step, keep each tiny), work them in order, mark each done. The user watches this list live.",
		"- databases (ask for DSN, never invent): db tables/schema/query. notebooks: notebook cells (execute needs jupyter). REST APIs: api (any method, JSON body).",
		"- open files in the editor: vscode open. new react/express/fastapi projects: scaffold.",
		"- local .gguf files: models info to inspect, models import to register with Ollama.",
		"Examples (copy the shape, raw text only):\n<tool:browser>{\"url\": \"https://example.com\"}</tool:browser>\n<tool:run>{\"language\": \"python\", \"code\": \"print(1)\"}</tool:run>",
		"- run shell commands only via bash (project dir, destructive cmds are blocked).",
		"- stay on task: call only the tools needed for THIS request. Don't explore the repo for fun.",
		"- a tool error shows its schema: fix your args and retry, don't abandon the task.\n",
	}
}

// joinRouting drops every line that teaches the delete tool when the mode
// doesn't have it, so no mode is told to use a tool it cannot call.
func joinRouting(lines []string, hasDelete bool) string {
	var b strings.Builder
	for _, ln := range lines {
		if !hasDelete && strings.Contains(strings.ToLower(ln), "delete") {
			continue
		}
		b.WriteString("\n" + ln)
	}
	return b.String()
}

func toolDocs(reg *tools.Registry, names []string, rounds int) string {
	if len(names) == 0 || reg == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nTOOLS — emit exactly: <tool:NAME>{\"arg\": \"value\"}</tool:NAME> (raw text, never fenced). Example: <tool:read>{\"path\": \"main.go\"}</tool:read>. One call per line; wait for results. Max %d rounds.\n", rounds)
	for _, n := range names {
		t, ok := reg.Get(n)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n  schema: %s\n", t.Name(), t.Description(), t.Schema())
	}
	return b.String()
}
