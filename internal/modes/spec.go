package modes

import (
	"fmt"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// spec is everything the harness knows about a mode, in one place.
//
// Adding a mode used to mean 28 coordinated edits: the const block, Parse's
// case list *and* its hardcoded error string, Order, Rounds, UsesRepoContext,
// AllowedTools, the SystemPrompt switch, the harness prose that lists the Tab
// cycle, five slash-command handlers, /help, the palette list, the palette icon
// switch, three TUI mode conditionals, two headless conditionals, two CLI flag
// help strings, the OpenAPI enum, and the test enumerations. Miss one and the
// mode is half-registered — a mode with no palette entry, or a Tab cycle that
// skips it, or a help string that never mentions it.
//
// The table is that list, as data. Order, Rounds, tool allow-lists, repo
// context, read-only, the slash command, the icon, the one-line description
// and the prompt all come from here, so a new mode is one entry plus a test.
type spec struct {
	mode Mode
	// slash is the command that selects this mode.
	slash string
	// icon groups it in the command palette.
	icon string
	// summary is the one-line description, used by /help and the palette.
	summary string
	// rounds is the per-turn tool-round budget. Zero means no tools.
	rounds int
	// tools is the allow-list, before MCP and plugin tools are appended.
	tools []string
	// repoContext adds the repo tree, brief and git state to the prompt.
	repoContext bool
	// readOnly blocks mutating tools for the whole turn.
	readOnly bool
	// prompt returns the MODE: block. It is a function because the block
	// embeds the tool docs, which depend on the model and the allow-list.
	prompt func(s spec, reg *tools.Registry, model string, rounds int) string
}

// buildTools is the full working set. It is a named var rather than an inline
// literal so goal mode can derive its own allow-list from it.
var buildTools = []string{
	"read", "write", "create", "add", "edit", "remove", "summary", "changes",
	"grep", "glob", "bash", "git", "github", "browser", "testgen", "security",
	"tree", "todo", "memory", "patch", "run", "delete", "db", "notebook",
	"api", "vscode", "scaffold", "models",
}

// without returns a copy of tools with the named entries removed. It never
// aliases the input, so a mode's allow-list cannot be mutated by another.
func without(tools []string, names ...string) []string {
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if !drop[t] {
			out = append(out, t)
		}
	}
	return out
}

// specs is ordered: the slice order is the Tab cycle (Order) and the order
// Names() reports, which is what the Parse error, the --mode help and the
// OpenAPI enum are all derived from.
var specs = []spec{
	{
		mode: Plan, slash: "/plan", icon: "⚡", summary: "read-only planning",
		rounds: 6, repoContext: true, readOnly: true,
		tools: []string{"read", "summary", "changes", "grep", "glob", "git", "browser", "security", "tree", "todo", "notebook", "api", "db", "models"},
		prompt: func(s spec, reg *tools.Registry, model string, rounds int) string {
			return "\nMODE: PLAN (read-only, planning only — never chat, never answer directly)." +
				" Research with read/grep/glob/git, then ALWAYS output a numbered step-by-step plan and end with 'AWAITING APPROVAL'." +
				"\nIf the request is ambiguous or missing key facts, FIRST ask up to 3 numbered clarifying questions (concise, each with your best-guess default) and stop — do not plan until the user answers." +
				" Do NOT write or edit files. For history use the git tool (log/diff/show/status work in plan mode) — bash is disabled here." +
				" Stay on task: don't repeat tool calls that already returned." + toolDocsFor(reg, s.tools, model, rounds)
		},
	},
	{
		mode: Goal, slash: "/goal", icon: "⚡", summary: "work a goal unattended",
		rounds: 16, repoContext: true,
		// Build's tools minus `delete`, derived rather than copied: an
		// unattended multi-iteration run should not recursively delete files
		// unattended. Remove through `remove`/bash, which are still available.
		// A second copy of this list was how a tool added to build would
		// silently never become available to a goal run.
		tools: without(buildTools, "delete"),
		prompt: func(s spec, reg *tools.Registry, model string, rounds int) string {
			rt := routingFor(false)
			if isSmallModel(model) {
				rt = shortRouting()
			}
			return "\nMODE: GOAL. You are working a GOAL the user stated and you run UNATTENDED. Act exactly like build mode: your first output is a tool call, not a sentence about one." +
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
				toolDocsFor(reg, s.tools, model, rounds)
		},
	},
	{
		mode: Build, slash: "/build", icon: "⚡", summary: "implement and test",
		rounds: 8, repoContext: true,
		tools: buildTools,
		prompt: func(s spec, reg *tools.Registry, model string, rounds int) string {
			rt := routing()
			if isSmallModel(model) {
				rt = shortRouting()
			}
			return "\nMODE: BUILD. START BUILDING IMMEDIATELY: first tool call does real work (orient with tree/glob, then create files) — no preamble questions, no explanations before acting." +
				" Ambiguity is resolved by reasonable defaults you state briefly AFTER the work, never by asking first (questions belong to plan mode)." +
				" Use tools to read, write, edit and verify code. After edits, re-read or run tests when sensible." + rt +
				toolDocsFor(reg, s.tools, model, rounds)
		},
	},
	{
		mode: Chat, slash: "/chat", icon: "⚡", summary: "plain conversation",
		prompt: func(spec, *tools.Registry, string, int) string {
			return "\nMODE: CHAT. Plain conversation, no tools."
		},
	},
	{
		mode: Thinking, slash: "/thinking", icon: "⚡", summary: "visible reasoning",
		prompt: func(spec, *tools.Registry, string, int) string {
			return "\nMODE: THINKING. Think step by step inside <scratchpad>...</scratchpad> (visible), then give the final answer. No tools in this mode — reason from conversation history."
		},
	},
}

var byMode = func() map[Mode]spec {
	m := make(map[Mode]spec, len(specs))
	for _, s := range specs {
		m[s.mode] = s
	}
	return m
}()

// lookup returns a mode's spec. An unknown mode falls back to chat, which has
// no tools, so a bad value fails safe.
func lookup(m Mode) spec {
	if s, ok := byMode[m]; ok {
		return s
	}
	return byMode[Chat]
}

// Known reports whether m is a registered mode.
func Known(m Mode) bool { _, ok := byMode[m]; return ok }

// Parse converts user input to a Mode.
func Parse(s string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(s)))
	if Known(m) {
		return m, nil
	}
	return "", fmt.Errorf("unknown mode %q (%s)", s, strings.Join(Names(), "/"))
}

// Names lists every mode name, in Tab-cycle order. The Parse error, the
// --mode flag help and the OpenAPI enum all read this, so they cannot fall out
// of step with the registry.
func Names() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, string(s.mode))
	}
	return out
}

// All returns every mode, in Tab-cycle order.
func All() []Mode {
	out := make([]Mode, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.mode)
	}
	return out
}

// Order is the Tab/Shift+Tab cycle.
func Order() []Mode { return All() }

// Slash returns the command that selects a mode.
func Slash(m Mode) string { return lookup(m).slash }

// Icon returns the command-palette icon group for a mode.
func Icon(m Mode) string { return lookup(m).icon }

// SlashForCommand returns the mode a slash command selects, or "" if the
// command is not a mode switch. Used by the palette's icon grouping.
func SlashForCommand(name string) Mode {
	for _, s := range specs {
		if s.slash == name {
			return s.mode
		}
	}
	return ""
}

// Summary is a mode's one-line description.
func Summary(m Mode) string { return lookup(m).summary }

// Rounds is the per-turn tool-round budget. Zero means the mode has no tools.
func Rounds(m Mode) int { return lookup(m).rounds }

// UsesRepoContext reports whether a mode sees the repo tree, codebase brief
// and git state in its system prompt.
func UsesRepoContext(m Mode) bool { return lookup(m).repoContext }

// IsReadOnly reports whether a mode blocks mutating tools for the whole turn.
func IsReadOnly(m Mode) bool { return lookup(m).readOnly }

// TabCycle is the human-readable cycle, for docs and /help. Derived from the
// table so it cannot drift.
func TabCycle() string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, strings.Title(string(s.mode))) //nolint:staticcheck // ASCII mode names
	}
	return strings.Join(out, " → ")
}

func AllowedTools(m Mode, extra ...string) []string {
	base := append([]string(nil), lookup(m).tools...)
	return append(base, extra...)
}

// toolDocsFor renders the tool documentation for an allow-list, choosing the
// compact form for models that drown in schemas.
func toolDocsFor(reg *tools.Registry, names []string, model string, rounds int) string {
	if isSmallModel(model) {
		return toolDocsCompact(reg, names, rounds)
	}
	return toolDocs(reg, names, rounds)
}

// DefaultGoalIters is how many autonomous turns a goal gets before the
// harness stops and reports progress so far.
const DefaultGoalIters = 12
