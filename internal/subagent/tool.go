package subagent

import (
	"context"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// ToolOptions configure the delegate handed to tools.TaskTool.
type ToolOptions struct {
	// Resolve supplies the provider and model at run time.
	//
	// A function rather than fields, because the registry outlives a turn: a
	// captured provider goes stale when the user switches model mid-session, and
	// refreshing a field before every turn is a step someone will forget.
	Resolve func() (providers.Provider, string)

	Registry *tools.Registry
	Workdir  string

	// Mode and Agent name the parent's context, so a subagent inherits a prompt
	// that fits rather than a generic one.
	Mode  string
	Agent string

	// Timeout bounds one subagent run.
	//
	// A subagent that hangs holds the parent's tool call open, and the parent can
	// neither see nor cancel it, so it has to be bounded from inside.
	Timeout time.Duration

	// OnEvent reports progress. Optional.
	OnEvent func(kind, text string)

	// Recorder shares the parent's trace, so /debug shows what the subagent was
	// asked. Without it a subagent looks like a tool call that did something.
	Recorder SubRecorder
}

// Delegate returns the function tools.TaskTool calls.
//
// The tool itself lives in internal/tools so it is registered in the catalog and
// a mode can allow-list it, while the behaviour lives here. Splitting it that way
// is what avoids the cycle: tools cannot import the agent loop, because the agent
// loop imports modes, and modes imports tools.
func Delegate(o ToolOptions) tools.DelegateFunc {
	return func(ctx context.Context, prompt, agentName string) (string, error) {
		if o.Resolve == nil {
			return "", errNotWired
		}
		prov, model := o.Resolve()
		if prov == nil {
			return "", errNoProvider
		}
		if agentName == "" {
			agentName = o.Agent
		}
		res := Run(ctx, prompt, Options{
			Provider: prov,
			Model:    model,
			Registry: o.Registry,
			Mode:     o.Mode,
			Agent:    agentName,
			Workdir:  o.Workdir,
			Timeout:  o.Timeout,
			OnEvent:  o.OnEvent,
			Recorder: o.Recorder,
		})
		if res.Err != nil {
			// Returned alongside any answer rather than instead of it: a subagent
			// that found something useful and then failed should still hand that
			// over.
			if res.Answer != "" {
				return res.Answer + "\n\n(subagent ended early: " + res.Err.Error() + ")", nil
			}
			return "", res.Err
		}
		return res.Answer, nil
	}
}

// Name is the tool name, exported so a registrar does not hard-code the string.
const Name = "task"

var (
	errNotWired   = errString("subagents are not available in this context")
	errNoProvider = errString("subagents need an active provider; none is configured")
)

type errString string

func (e errString) Error() string { return string(e) }
