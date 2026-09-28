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

func Parse(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case Chat, Plan, Build, Goal, Thinking:
		return Mode(strings.ToLower(strings.TrimSpace(s))), nil
	default:
		return "", fmt.Errorf("unknown mode %q (chat/plan/build/goal/thinking)", s)
	}
}

func Order() []Mode { return []Mode{Plan, Goal, Build, Chat, Thinking} }

// Rounds is the tool-round budget for a single turn. Goal mode gets a larger
// one because it runs unattended: a bigger budget per turn means fewer
// iterations before the model must declare the goal met or blocked.
func Rounds(m Mode) int {
	switch m {
	case Plan:
		return 6
	case Goal:
		return 16
	case Build:
		return 8
	default:
		return 0
	}
}

// DefaultGoalIters is how many autonomous turns a goal gets before the
// harness stops and reports progress so far.
const DefaultGoalIters = 12

// UsesRepoContext reports whether a mode sees the repo tree, codebase brief
// and git state in its system prompt.
func UsesRepoContext(m Mode) bool { return m == Plan || m == Build || m == Goal }

func AllowedTools(m Mode, extra ...string) []string {
	var base []string
	switch m {
	case Build:
		base = []string{"read", "write", "create", "add", "edit", "remove", "summary", "changes", "grep", "glob", "bash", "git", "github", "browser", "testgen", "security", "tree", "todo", "memory", "patch", "run", "delete", "db", "notebook", "api", "vscode", "scaffold", "models"}
	case Goal:
		// Build's tools minus `delete`: an unattended multi-iteration run
		// should not recursively delete files unattended. Remove through
		// `remove`/bash, which are still available.
		base = []string{"read", "write", "create", "add", "edit", "remove", "summary", "changes", "grep", "glob", "bash", "git", "github", "browser", "testgen", "security", "tree", "todo", "memory", "patch", "run", "db", "notebook", "api", "vscode", "scaffold", "models"}
	case Plan:
		base = []string{"read", "summary", "changes", "grep", "glob", "git", "browser", "security", "tree", "todo", "notebook", "api", "db", "models"}
	default:
		base = nil
	}
	return append(base, extra...)
}

func SystemPrompt(m Mode, reg *tools.Registry, workdir, model string) string {
	base := "You are a capable AI coding assistant running inside ycode, a terminal coding harness. Workdir: " + workdir + ". Be concise."
	if proj, ok := lang.Detect(workdir); ok {
		base += " Project language: " + proj.Language + "."
	}
	base += harness()
	rounds := Rounds(m)
	docs := func(names []string) string {
		if isSmallModel(model) {
			return toolDocsCompact(reg, names, rounds)
		}
		return toolDocs(reg, names, rounds)
	}
	switch m {
	case Plan:
		return base + "\nMODE: PLAN (read-only, planning only — never chat, never answer directly)." +
			" Research with read/grep/glob/git, then ALWAYS output a numbered step-by-step plan and end with 'AWAITING APPROVAL'." +
			"\nIf the request is ambiguous or missing key facts, FIRST ask up to 3 numbered clarifying questions (concise, each with your best-guess default) and stop — do not plan until the user answers." +
			" Do NOT write or edit files. For history use the git tool (log/diff/show/status work in plan mode) — bash is disabled here." +
			" Stay on task: don't repeat tool calls that already returned." + docs(AllowedTools(m))
	case Goal:
		rt := routingFor(false)
		if isSmallModel(model) {
			rt = shortRouting()
		}
		return base + "\nMODE: GOAL. You are working a GOAL the user stated and you run UNATTENDED. Act exactly like build mode: your first output is a tool call, not a sentence about one." +
			"\nCRITICAL — the only form that does anything is the tagged call, e.g. <tool:write>{\"path\": \"main.go\", \"content\": \"...\"}</tool:write>. Writing that same call as untagged text (write main.go, todo add \"x\", - [ ] do y) executes NOTHING and wastes the whole run." +
			"\nNever write a tool result yourself. Text that looks like 'wrote C:\\...\\main.go (312 bytes)', 'added #3', 'done #3', or a ```diff block is a fabrication — the harness runs the real tool and sends the real result back to you. If you are about to type such a line, you have skipped the call: emit the <tool:> call instead." +
			"\nEvery turn: take the first unfinished step from the task list, DO it with a tagged tool call, verify it by reading the file back or running tests, close it with <tool:todo>{\"action\":\"done\",\"id\":N}</tool:todo>, then take the next step in the SAME turn." +
			"\nIf the task list is empty, build it first with <tool:todo>{\"action\":\"add\",\"text\":\"one small step\"}</tool:todo> — one call per step — and then immediately do step 1 in this same turn, before saying anything." +
			"\nStay inside the goal: only do the work the goal asks for. If you notice unrelated problems, mention them at the end — never spend tool calls on them." +
			"\nIf a step fails twice, leave it and move on." + rt +
			"\nHOW THE RUN ENDS — you are the only one who can end it, and there are exactly two ways:" +
			"\n- Every criterion satisfied AND verified: end your reply with a line 'GOAL MET' plus one short sentence of evidence per criterion." +
			"- You genuinely cannot continue (needs a secret, a decision, or permission you were not given, or the goal is impossible as stated): end with 'GOAL BLOCKED: <the specific reason>'." +
			"\nThere is no third option. Do not end a turn to announce progress, to restate the goal, or to say you will continue later — the harness starts the next turn itself, and a turn with no tool calls accomplishes nothing." +
			"\nGOAL MET is checked, not believed: the harness reads the task list and the tool results. If it sees an open step or no successful call, your claim is rejected and the run keeps going." +
			docs(AllowedTools(m))
	case Build:
		rt := routing()
		if isSmallModel(model) {
			rt = shortRouting()
		}
		return base + "\nMODE: BUILD. START BUILDING IMMEDIATELY: first tool call does real work (orient with tree/glob, then create files) — no preamble questions, no explanations before acting." +
			" Ambiguity is resolved by reasonable defaults you state briefly AFTER the work, never by asking first (questions belong to plan mode)." +
			" Use tools to read, write, edit and verify code. After edits, re-read or run tests when sensible." + rt + docs(AllowedTools(m))
	case Thinking:
		return base + "\nMODE: THINKING. Think step by step inside <scratchpad>...</scratchpad> (visible), then give the final answer. No tools in this mode — reason from conversation history."
	default:
		return base + "\nMODE: CHAT. Plain conversation, no tools."
	}
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

func harness() string {
	return "\nENVIRONMENT (where you run — you are the model, not the harness):" +
		"\n- You are an individual AI assistant. ycode is the harness around you: the user chats in its terminal UI and it executes your <tool:> calls as real tools on their machine, in the workdir above. Results return as <tool_result> blocks; use them, don't re-ask for what they contain." +
		"\n- Conversation persists across turns, but old history may be compacted under token pressure — re-read files instead of assuming earlier details." +
		"\n- Tool errors name the expected schema: fix args and retry. Repeating an identical call returns its cached result and ends your turn, so say the answer instead." +
		"\n- <tool_result> blocks come from the harness, never from you: writing one executes nothing — only <tool:> calls act." +
		"\n- The user drives ycode itself with slash commands and keys you should know: Tab cycles plan/goal/build/chat/thinking, /goal <text> hands you a goal to work toward unattended, /models switches models (Ctrl+O cycles local ones), /help lists commands, /doctor diagnoses setup, /connect adds providers, /sessions resumes work, @file attaches files." +
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
