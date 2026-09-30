package subagent

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/agent"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Subagents.
//
// A subagent answers a question in its own context window and returns a summary,
// so the parent's conversation is not filled with a hundred grep results.
//
// That is the whole benefit and it is a real one: "search the repo for every
// place this is called" is one parent tool call and a summary line, rather than
// 40KB of matches competing for the context the rest of the work needs.
//
// The risk is the obvious one. A subagent with the parent's tools could quietly
// rewrite the user's files while the parent believes it only asked a question. So
// delegation is opt-in per tool through the Isolatable interface, and the
// built-ins that opt in are the ones that only read. This is not a sandbox and
// does not pretend to be - it is a narrower allow-list, enforced at the same
// place the mode's allow-list is.

// Options configure one subagent run.
type Options struct {
	// Provider and Model are the parent's. A subagent uses the same ones: making
	// it choose differently would mean its answers came from a model the user
	// did not choose, which is worse than no subagent.
	Provider providers.Provider
	Model    string
	Registry *tools.Registry

	// Mode is the parent's, used only to derive the system prompt. The
	// allow-list is not taken from it - see Allow.
	// A string rather than modes.Mode, because the tool that carries it lives in a
	// package that cannot import modes without a cycle: modes imports tools.
	Mode string
	// Agent names the persona, e.g. "explorer".
	Agent string

	// Workdir bounds the subagent to the same tree the parent is in.
	Workdir string

	// Rounds is how many tool rounds the subagent gets. Lower than the parent's
	// by default: it is answering one question, not doing the task.
	Rounds int

	// Timeout bounds the whole run. A subagent that hangs holds a tool call in
	// the parent's turn, and the parent has no way to see or cancel it.
	Timeout time.Duration

	// OnEvent reports progress, for a UI. Optional.
	OnEvent func(kind, text string)

	// Recorder, when set, receives the subagent's prompt and answer. It shares
	// the parent's recorder so /debug shows what the subagent was asked, which
	// is otherwise invisible: without it a subagent looks like a tool call that
	// did something.
	Recorder SubRecorder
}

// SubRecorder is the slice of the trace recorder this package needs, declared
// here so it does not import internal/trace and force a dependency from the
// runner onto the TUI's package.
type SubRecorder interface {
	Note(turn int, text string)
}

// Result is what the parent gets back.
type Result struct {
	// Answer is the summary. Only this crosses back into the parent's context.
	Answer string
	// Calls is how many tools the subagent used, for the parent's stats.
	Calls int
	// Rounds is how many model turns it took.
	Rounds int
	// Err is set when the run failed.
	Err error
}

// agentNames are the personas a subagent can take.
//
// Deliberately few and deliberately read-shaped. A catalogue of twenty would be
// twenty ways for the parent model to pick the wrong one, and each would need its
// own prompt to be worth having.
//
// Every persona states that it cannot change anything. That is not decoration: a
// subagent told only to "analyse" will sometimes report that it fixed something,
// and the parent will believe it, because the parent's own transcript shows the
// subagent claiming to have written a file.
var agentNames = map[string]string{
	"explorer": "You are a code explorer. Search the repository and report what you find. " +
		"You cannot change anything, so never claim to have made a change.",
	"analyst": "You are a code analyst. Read and explain: how something works, where it is used, " +
		"what would break if it changed. Report facts and quote file:line. " +
		"You cannot change anything, so never claim to have made a change.",
}

// Agents lists the available personas.
func Agents() []string {
	out := make([]string, 0, len(agentNames))
	for k := range agentNames {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AgentNames is the alias used by the tool's schema.
func AgentNames() string { return strings.Join(Agents(), ", ") }

// Allow returns the tools a subagent may use.
//
// The intersection of what the parent is allowed and what is delegable, which is
// the important half: a subagent must never be able to reach a tool the parent
// itself could not. Deriving it from the parent rather than from the full
// delegable set is what makes delegation a narrowing instead of a widening.
func Allow(parentAllowed []string, reg *tools.Registry) []string {
	var out []string
	for _, n := range parentAllowed {
		if !tools.IsIsolatable(n, nil) {
			continue
		}
		if t, ok := reg.Get(n); !ok || !tools.IsIsolatable(n, t) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// delegable lists what a subagent could use at all, for an error message when
// the parent's allow-list contains nothing delegable.
func delegable() []string {
	var out []string
	for _, n := range []string{"read", "grep", "glob", "tree", "changes", "summary", "security", "todo", "memory", "models", "browser"} {
		if tools.IsIsolatable(n, nil) {
			out = append(out, n)
		}
	}
	return out
}

const (
	// defaultRounds is fewer than the parent's 8. A subagent answers one
	// question; if it needs more than a handful of searches the question was
	// probably badly posed, and the parent is better served by doing it itself
	// with the results in context.
	defaultRounds = 4

	// defaultTimeout bounds a run. The parent's timeout does not apply, because
	// the subagent's time is spent inside one of the parent's tool calls.
	defaultTimeout = 3 * time.Minute

	// maxAnswerBytes caps what crosses back.
	//
	// The entire premise is that the parent's context stays small. A subagent
	// that returns 200KB of grep output defeats it completely, and the model will
	// happily do exactly that if not stopped.
	maxAnswerBytes = 8 << 10
)

// Run executes one subagent and returns its summary.
func Run(ctx context.Context, prompt string, o Options) Result {
	if o.Provider == nil || o.Registry == nil {
		return Result{Err: fmt.Errorf("subagent: provider and registry are required")}
	}
	persona, ok := agentNames[strings.ToLower(strings.TrimSpace(o.Agent))]
	if !ok {
		// Defaulted rather than refused: an unknown persona is a model mistake,
		// and failing the parent's whole turn over it is worse than running with
		// the general one. The event says which was used.
		persona = agentNames["explorer"]
		if o.OnEvent != nil {
			o.OnEvent("agent", "unknown agent "+o.Agent+", using explorer")
		}
	}

	parentAllowed := modes.AllowedTools(modes.Mode(o.Mode))
	allow := Allow(parentAllowed, o.Registry)
	if len(allow) == 0 {
		// Said in full, because "subagents are not available in this mode" leaves
		// the user guessing whether the feature is broken or their mode is
		// wrong.
		return Result{Err: fmt.Errorf(
			"no delegable tools in %s mode. A subagent can only read; "+
				"delegable tools are: %s", o.Mode, strings.Join(delegable(), ", "))}
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rounds := o.Rounds
	if rounds <= 0 {
		rounds = defaultRounds
	}

	sys := agent.BuildSystem(agent.SystemOptions{
		Mode:     modes.Mode(o.Mode),
		Registry: o.Registry,
		Workdir:  o.Workdir,
		Model:    o.Model,
		Agent:    o.Agent,
		Headless: true,
		// ZeroLeak is deliberately not passed. The subagent's allow-list is
		// already read-only and the parent has applied whatever network policy
		// it was configured with; applying it twice is harmless but inheriting it
		// would mean a zero-data-leak parent could not delegate at all, since the
		// only delegable tool that reaches the network is browser.
	})
	// The persona replaces the standard opening rather than appending to it: two
	// instructions about how to behave, one of them generic, is how a system
	// prompt becomes wallpaper.
	sys = persona + "\n\n" + sys

	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: sys},
		{Role: apitypes.RoleUser, Content: prompt},
	}
	if o.Recorder != nil {
		o.Recorder.Note(0, fmt.Sprintf("subagent %s asked: %s", o.Agent, prompt))
	}
	if o.OnEvent != nil {
		o.OnEvent("start", fmt.Sprintf("subagent %s starting (%d tool(s))", o.Agent, len(allow)))
	}

	// Nothing is written to the parent's stream. The subagent's intermediate
	// steps are not the user's business, and writing them would defeat the
	// context saving by putting them in the transcript.
	obs := &agent.Observer{
		OnChunk: func(name, chunk string) {
			if o.OnEvent != nil {
				o.OnEvent("tool", name+": "+chunk)
			}
		},
	}
	res, err := agent.RunWithRounds(ctx, o.Provider, o.Model, msgs, o.Registry,
		allow, nil, io.Discard, obs, rounds, string(modes.Plan))

	answer := res.Text
	if err != nil {
		// The error is returned rather than folded into the answer: the parent
		// needs to be able to tell "the subagent found nothing" from "the
		// subagent broke", and only one of those should be retried.
		if o.Recorder != nil {
			o.Recorder.Note(0, "subagent failed: "+err.Error())
		}
		if o.OnEvent != nil {
			o.OnEvent("error", err.Error())
		}
		return Result{Answer: answer, Calls: res.Calls, Rounds: res.Rounds, Err: err}
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "(the subagent returned nothing — try a more specific question)"
	}
	if len(answer) > maxAnswerBytes {
		// Keep the head, not the tail: the answer states what was found before
		// any detail, and cutting the end leaves the conclusion intact. Truncated
		// on a rune boundary - a byte cut here produces mojibake in the parent's
		// context, which is the one place it will be read.
		answer = textutil.TruncateBytes(answer, maxAnswerBytes/2) +
			fmt.Sprintf("\n…(answer truncated from %d bytes; ask a narrower question)",
				len(answer))
	}
	if o.Recorder != nil {
		o.Recorder.Note(0, fmt.Sprintf("subagent %s answered in %d round(s), %d call(s): %s",
			o.Agent, res.Rounds, res.Calls, answer))
	}
	if o.OnEvent != nil {
		o.OnEvent("done", fmt.Sprintf("subagent %s finished (%d call(s))", o.Agent, res.Calls))
	}
	return Result{Answer: answer, Calls: res.Calls, Rounds: res.Rounds}
}
