package headless

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/agent"
	"github.com/Yash-K-Jagani/ycode/internal/agents"
	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/webhooks"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Options struct {
	Mode    modes.Mode
	Agent   string
	Workdir string
	Timeout time.Duration
	Stderr  io.Writer
	// GoalIters overrides the goal-mode iteration budget (0 = default).
	GoalIters int
}

// goalMaxIters is the headless goal budget; short compared with the TUI
// because nobody is watching a CI run spend an hour of tokens.
const goalMaxIters = 6

// openSteps counts unfinished task-list entries: in goal mode this is the
// harness's independent check on a claimed GOAL MET.
func openSteps(workdir string) int {
	n := 0
	for _, t := range tools.ReadTodos(workdir) {
		if !t.Done {
			n++
		}
	}
	return n
}

func (o *Options) withDefaults() {
	if o.Mode == "" {
		o.Mode = modes.Build
	}
	if o.Agent == "" {
		o.Agent = "builder"
	}
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Minute
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
}

// Outcome is how a run ended. It is only meaningful in goal mode, where a run
// can finish without the goal being met — which the exit code and the HTTP API
// both need to report, because "exited 0" previously meant "the model claimed
// it was done" as readily as "the work is done".
type Outcome string

const (
	// OutcomeNone is any non-goal run: there is no pass/fail notion.
	OutcomeNone Outcome = ""
	// OutcomeMet: the goal was met, and the evidence backed it.
	OutcomeMet Outcome = "met"
	// OutcomeBlocked: the model reported it could not continue.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeExhausted: the iteration budget ran out with work still owed.
	OutcomeExhausted Outcome = "budget_exhausted"
	// OutcomeStalled: a turn ran with no tool calls, so nothing was done.
	OutcomeStalled Outcome = "stalled"
	// OutcomeCancelled: the caller cancelled the run.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeUnverified: the run ended while the goal was still active, for a
	// reason not otherwise classified.
	OutcomeUnverified Outcome = "unverified"
)

// ExitCode maps an outcome to a process exit status, for CI use.
// 0 means the work is done; everything else is a distinct, documented failure.
func (o Outcome) ExitCode() int {
	switch o {
	case OutcomeNone, OutcomeMet:
		return 0
	case OutcomeBlocked:
		return 2
	case OutcomeExhausted:
		return 3
	case OutcomeStalled:
		return 4
	case OutcomeCancelled:
		return 5
	default:
		return 6
	}
}

// Run executes a single headless turn: system prompt + optional tool loop.
// In goal mode it instead runs the goal loop: repeated turns until the model
// reports GOAL MET / GOAL BLOCKED or the iteration budget runs out.
func Run(ctx context.Context, cfg config.Config, prompt string, o Options) (string, error) {
	answer, _, err := RunWithStatus(ctx, cfg, prompt, o)
	return answer, err
}

// RunWithStatus is Run plus how the run ended, so callers that care (the CLI's
// exit code, the HTTP API's response) can tell a met goal from a model that
// merely claimed one.
func RunWithStatus(ctx context.Context, cfg config.Config, prompt string, o Options) (string, Outcome, error) {
	o.withDefaults()
	if o.Mode == modes.Goal {
		return runGoal(ctx, cfg, prompt, o)
	}
	answer, err := runTurn(ctx, cfg, prompt, o)
	if err != nil {
		return answer, OutcomeNone, err
	}
	return answer, OutcomeNone, nil
}

func runTurn(ctx context.Context, cfg config.Config, prompt string, o Options) (string, error) {
	o.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if cfg.ZeroDataLeak {
		ctx = tools.WithZeroLeak(ctx)
	}
	r := router.New(cfg)
	ag, ok := agents.Get(o.Agent)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", o.Agent)
	}
	reg := tools.DefaultRegistry(o.Workdir)
	allowed := modes.AllowedTools(o.Mode)
	sys := modes.SystemPrompt(o.Mode, reg, o.Workdir, cfg.ActiveModel) + "\nActive agent: " + ag.Name + " — " + ag.Prompt +
		"\nHEADLESS: no interactive user. Do the task, verify with tools, output the final result."
	if modes.UsesRepoContext(o.Mode) {
		sys += "\nRepo tree (" + o.Workdir + ") — real paths, use them directly:\n" + yctx.Tree(o.Workdir, 150, 4000) +
			"NEVER ask the user for paths or locations. If a file is named without a path, find it with glob/grep yourself."
		if brief := yctx.Brief(o.Workdir); brief != "" {
			sys += "\nCodebase brief (what this repo is, its rules, git state):\n" + brief
		}
	}
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: sys},
		{Role: apitypes.RoleUser, Content: prompt},
	}
	trimmed, _ := yctx.Trim(msgs[1:], yctx.BudgetFor(cfg.ActiveModel)-1500)
	msgs = append(msgs[:1], trimmed...)
	hookset := hooks.Load()
	if o.Mode == modes.Plan {
		ctx = tools.WithReadOnly(ctx)
	}
	hookset.Fire(ctx, hooks.OnRequest, map[string]string{"mode": string(o.Mode), "workdir": o.Workdir, "headless": "true"})
	log := o.Stderr
	if len(allowed) == 0 {
		full, _, err := r.StreamWithFallback(ctx, msgs, io.Discard)
		if err != nil {
			hookset.Fire(ctx, hooks.OnError, map[string]string{"error": err.Error()})
			return "", err
		}
		hookset.Fire(ctx, hooks.OnResponse, map[string]string{"mode": string(o.Mode)})
		audit.Log("headless_turn", map[string]any{"mode": string(o.Mode), "prompt": prompt, "answer": full})
		return full, nil
	}
	p, model, err := r.Active()
	if err != nil {
		return "", err
	}
	res, err := agent.RunWithRounds(ctx, p, model, msgs, reg, allowed, hookset, log, func(name, args, result string, err error) {
		status := "ok"
		if err != nil {
			status = "ERR " + err.Error()
		}
		_, _ = fmt.Fprintf(log, "[tool %s] %s\n", name, status)
	}, modes.Rounds(o.Mode), string(o.Mode))
	_ = log
	if err != nil && res.Text == "" {
		// one fallback attempt on cloud providers
		fbs := r.Fallbacks()
		if len(fbs) == 0 {
			return "", err
		}
		fp, ferr := r.Provider(fbs[0].Provider)
		if ferr != nil {
			return "", err
		}
		res, err = agent.RunWithRounds(ctx, fp, fbs[0].Model, msgs, reg, allowed, hookset, io.Discard, nil, modes.Rounds(o.Mode), string(o.Mode))
		if err != nil && res.Text == "" {
			return "", err
		}
		res.Text += "\n\n(fell back to " + fbs[0].Provider + "/" + fbs[0].Model + ")"
	}
	hookset.Fire(ctx, hooks.OnResponse, map[string]string{"mode": string(o.Mode)})
	audit.Log("headless_turn", map[string]any{"mode": string(o.Mode), "prompt": prompt, "answer": res.Text})
	return res.Text, nil
}

// runGoal drives the autonomous goal loop headlessly: the goal text is the
// prompt, and iterations continue until the model reports GOAL MET / GOAL
// BLOCKED or MaxIter turns have run.
func runGoal(ctx context.Context, cfg config.Config, prompt string, o Options) (string, Outcome, error) {
	o.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if cfg.ZeroDataLeak {
		ctx = tools.WithZeroLeak(ctx)
	}
	iters := o.GoalIters
	if iters <= 0 {
		iters = goalMaxIters
	}
	g := goal.New(prompt, iters)
	r := router.New(cfg)
	ag, ok := agents.Get(o.Agent)
	if !ok {
		return "", OutcomeUnverified, fmt.Errorf("unknown agent %q", o.Agent)
	}
	reg := tools.DefaultRegistry(o.Workdir)
	allowed := modes.AllowedTools(o.Mode)
	base := modes.SystemPrompt(o.Mode, reg, o.Workdir, cfg.ActiveModel) + "\nActive agent: " + ag.Name + " — " + ag.Prompt +
		"\nHEADLESS: no interactive user. Do the task, verify with tools, output the final result."
	if modes.UsesRepoContext(o.Mode) {
		base += "\nRepo tree (" + o.Workdir + ") — real paths, use them directly:\n" + yctx.Tree(o.Workdir, 150, 4000) +
			"NEVER ask the user for paths or locations. If a file is named without a path, find it with glob/grep yourself."
		if brief := yctx.Brief(o.Workdir); brief != "" {
			base += "\nCodebase brief (what this repo is, its rules, git state):\n" + brief
		}
	}
	hookset := hooks.Load()
	log := o.Stderr
	rounds := modes.Rounds(o.Mode)

	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: base + g.PromptBlock("", "")},
		{Role: apitypes.RoleUser, Content: g.Text},
	}
	hookset.Fire(ctx, hooks.OnRequest, map[string]string{"mode": string(o.Mode), "workdir": o.Workdir, "headless": "true"})

	var last string
	for {
		select {
		case <-ctx.Done():
			g.Status = goal.Cancelled
			return last + "\n\n(stopped: " + ctx.Err().Error() + ")\n" + g.StopNote(), OutcomeCancelled, ctx.Err()
		default:
		}
		trimmed, _ := yctx.Trim(msgs[1:], yctx.BudgetFor(cfg.ActiveModel)-1500)
		turnMsgs := append([]apitypes.Message{msgs[0]}, trimmed...)
		p, model, err := r.Active()
		if err != nil {
			return "", OutcomeUnverified, err
		}
		workOK := 0
		res, err := agent.RunWithRounds(ctx, p, model, turnMsgs, reg, allowed, hookset, log, func(name, args, result string, err error) {
			status := "ok"
			if err != nil {
				status = "ERR " + err.Error()
			} else if goal.IsWorkTool(name) {
				workOK++
			}
			_, _ = fmt.Fprintf(log, "[goal %d/%d tool %s] %s\n", g.Iter+1, g.MaxIter, name, status)
		}, rounds, string(o.Mode))
		if err != nil && res.Text == "" {
			return "", OutcomeUnverified, err
		}
		last = res.Text
		audit.Log("goal_iteration", map[string]any{"iteration": g.Iter + 1, "prompt": g.Text, "answer": res.Text})
		// Check the model's verdict against what actually happened: small
		// models claim GOAL MET on autopilot, and narrate instead of acting.
		g.Reconcile(res.Text, goal.Evidence{
			Calls:          res.Calls,
			SucceededCalls: res.OKs,
			FailedCalls:    res.Failed,
			WorkCalls:      workOK,
			OpenSteps:      openSteps(o.Workdir),
		})
		if g.Rejected() {
			_, _ = fmt.Fprintf(log, "%s\n", g.RejectionNote())
		}
		if !g.Next() {
			break
		}
		msgs = append(msgs,
			apitypes.Message{Role: apitypes.RoleAssistant, Content: res.Text},
			apitypes.Message{Role: apitypes.RoleSystem, Content: g.Continuation()})
	}
	hookset.Fire(ctx, hooks.OnResponse, map[string]string{"mode": string(o.Mode)})
	audit.Log("headless_goal", map[string]any{"goal": g.Text, "status": string(g.Status), "iterations": g.Iter, "answer": last})
	_, _ = fmt.Fprintf(log, "goal: %s\n", g.Summary())
	// Headless runs used to emit no webhooks at all, so a CI goal run was
	// invisible to anything watching the session events. Skipped under
	// Zero-Data-Leak, which is the point of that mode.
	if !cfg.ZeroDataLeak {
		webhooks.Fire("turn_complete", map[string]any{
			"mode": string(o.Mode), "goal_status": string(g.Status),
			"iterations": g.Iter, "goal": g.Text,
		})
	}
	return last + "\n\n" + g.StopNote(), outcomeOf(g.Status), nil
}

// outcomeOf maps a terminal goal status to a caller-visible outcome.
func outcomeOf(s goal.Status) Outcome {
	switch s {
	case goal.Met:
		return OutcomeMet
	case goal.Blocked:
		return OutcomeBlocked
	case goal.Spent:
		return OutcomeExhausted
	case goal.Stalled:
		return OutcomeStalled
	case goal.Cancelled:
		return OutcomeCancelled
	default:
		return OutcomeUnverified
	}
}
