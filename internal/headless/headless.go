package headless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/agent"
	"github.com/Yash-K-Jagani/ycode/internal/agents"
	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
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

	// Tracker records this run's spend. Nil means a fresh one, which is right in
	// production: the tracker is persisted, so a fresh one still sees the whole
	// day's total and the budget still binds across runs.
	//
	// It is a field so a test can supply an in-memory tracker. Reading the
	// persisted record is correct behaviour and untestable behaviour, and
	// without this seam every budget test also depends on whatever the developer
	// spent that morning.
	Tracker *cost.Tracker
}

// goalMaxIters is the headless goal budget; short compared with the TUI
// because nobody is watching a CI run spend an hour of tokens.
const goalMaxIters = 6

// openSteps counts unfinished task-list entries: in goal mode this is the
// harness's independent check on a claimed GOAL MET.
func openSteps(workdir string) int {
	return goal.OpenSteps(taskList(workdir))
}

// taskList reads the workdir task list into the goal package's own type, so
// the two run paths (TUI and headless) cannot disagree about what "open
// steps" means.
func taskList(workdir string) []goal.TaskList {
	items := tools.ReadTodos(workdir)
	out := make([]goal.TaskList, 0, len(items))
	for _, it := range items {
		out = append(out, goal.TaskList{ID: it.ID, Text: it.Text, Done: it.Done})
	}
	return out
}

func todoBlock(workdir string) string { return goal.TaskBlock(taskList(workdir)) }

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
	if o.Tracker == nil {
		o.Tracker = cost.New()
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
	// OutcomeSpendLimited: the daily spend budget was reached.
	//
	// Deliberately distinct from OutcomeExhausted, which is the iteration
	// budget. Both are "stopped early, work still owed" and a CI job needs to
	// tell them apart: exhausted means raise MaxIter, spend-limited means raise
	// daily_budget_usd or change provider. Folding them together would send
	// someone to fix the wrong setting.
	OutcomeSpendLimited Outcome = "spend_limited"
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
	case OutcomeSpendLimited:
		// Its own code so a CI job can tell "raise MaxIter" apart from "raise
		// daily_budget_usd" without parsing the log.
		return 7
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
	// The daily budget is shared across entry points. The tracker is persisted,
	// so a headless run started after a long TUI session is held to the same
	// ceiling, and an unattended `ycode ops` cannot be used to spend past a
	// limit the user set.
	//
	// It is now enforceable for headless spend too: this run records its own
	// token counts via recordUsage, so the tracker it enforces against includes
	// what it spent.
	if err := overBudget(cfg, o.Tracker, o.Stderr); err != nil {
		return "", err
	}
	if cfg.ZeroDataLeak {
		ctx = tools.WithZeroLeak(ctx)
	}
	r := router.New(cfg)
	ag, ok := agents.Get(o.Agent)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", o.Agent)
	}
	reg := tools.DefaultRegistry(o.Workdir)
	// Wired so an unattended run can delegate too; see task.go for why it is
	// omitted under zero-data-leak.
	addTaskTool(reg, r, cfg, o)
	allowed := modes.AllowedTools(o.Mode)
	sys := agent.BuildSystem(agent.SystemOptions{
		Mode:     o.Mode,
		Registry: reg,
		Workdir:  o.Workdir,
		Model:    cfg.ActiveModel,
		Agent:    ag.Name,
		Headless: true,
		ZeroLeak: cfg.ZeroDataLeak,
	})
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: sys},
		{Role: apitypes.RoleUser, Content: prompt},
	}
	trimmed, _ := yctx.Trim(msgs[1:], yctx.BudgetFor(cfg.ActiveModel)-1500)
	msgs = append(msgs[:1], trimmed...)
	hookset := hooks.Load()
	if modes.IsReadOnly(o.Mode) {
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
	// One chain implementation, shared with the TUI. This used to try only
	// fallbacks[0] and to drop the tool log on that attempt, so a second
	// fallback never ran and a fallback's tool calls were invisible.
	chain := []agent.Candidate{{Provider: p, Model: model}}
	for _, fb := range r.Fallbacks() {
		if fp, ferr := r.Provider(fb.Provider); ferr == nil {
			chain = append(chain, agent.Candidate{Provider: fp, Model: fb.Model, Label: fb.Provider + "/" + fb.Model})
		}
	}
	res := agent.RunChain(ctx, chain, msgs, reg, allowed, hookset, log, &agent.Observer{
		OnTool: func(name, args, result string, err error) {
			status := "ok"
			if err != nil {
				status = "ERR " + err.Error()
			}
			_, _ = fmt.Fprintf(log, "[tool %s] %s\n", name, status)
		},
		// Tool output as it happens, on the same stream the assistant's text
		// already uses. A CI log that goes silent for the four minutes a test
		// suite takes is indistinguishable from a hung job, and the person
		// watching has no way to tell those apart.
		OnChunk: func(name, chunk string) {
			_, _ = fmt.Fprintf(log, "[%s] %s", name, chunk)
		},
	}, modes.Rounds(o.Mode), string(o.Mode), func(label string) {
		_, _ = fmt.Fprintf(log, "retrying turn on fallback %s\n", label)
	})
	if res.Text == "" {
		err := res.Failure()
		hookset.Fire(ctx, hooks.OnError, map[string]string{"error": err.Error()})
		return "", err
	}
	answer := res.Text
	if res.Err != nil {
		answer += "\n\n(stopped early: " + res.Err.Error() + ")"
	}
	if res.FellBack() {
		answer += "\n\n(fell back to " + res.Used + ")"
	}
	hookset.Fire(ctx, hooks.OnResponse, map[string]string{"mode": string(o.Mode)})
	if !cfg.ZeroDataLeak {
		webhooks.Fire("turn_complete", map[string]any{"mode": string(o.Mode)})
	}
	recordUsage(cfg, o.Tracker, r, log)
	audit.Log("headless_turn", map[string]any{"mode": string(o.Mode), "prompt": prompt, "answer": answer})
	return answer, nil
}

// recordUsage adds this turn's tokens to the shared spend tracker.
//
// Headless recorded nothing before this. That was not a cosmetic gap: the
// tracker is what `daily_budget_usd` enforces against, so an unattended
// `ycode -p` spent money that no limit could see and a budget set in the TUI was
// quietly bypassed by running the same work from a script.
//
// It also means `/status` and the sidebar now show CI and script spend, rather
// than only what was typed into the TUI.
//
// The provider is taken from the usage rather than the config, because a turn
// that fell back to Gemini was priced by Gemini and costing it as Ollama would
// report $0.
func recordUsage(cfg config.Config, tr *cost.Tracker, r *router.Router, log io.Writer) {
	u := r.TakeUsage()
	provider := u.Provider
	if provider == "" {
		provider = cfg.ActiveProvider
	}
	if !u.Reported {
		// Say so on the log. A cost line that looks measured but is a guess is
		// the thing this whole change exists to remove, and the log is where
		// someone debugging a bill will look.
		_, _ = fmt.Fprintf(log, "[usage] provider %s reported no token counts; cost is unmeasured\n", provider)
		return
	}
	usd := tr.Add(provider, u.PromptTok, u.ComplTok)
	_, _ = fmt.Fprintf(log, "[usage] %s: %d prompt + %d completion tokens, $%.4f\n",
		provider, u.PromptTok, u.ComplTok, usd)
}

// overBudget reports whether the day's recorded spend has reached the limit, and
// says why with the number attached.
//
// Called between goal iterations as well as before a turn, so an unattended run
// stops at a boundary where the reason is visible in the log.
func overBudget(cfg config.Config, tr *cost.Tracker, log io.Writer) error {
	// cost.New, not a stored budget: the limit can be raised between iterations and
	// re-reading it means the run picks that up without a restart.
	b := cost.NewBudget(tr, cfg.DailyBudgetUSD)
	if b.Exceeded() {
		msg := b.BlockedMessage()
		_, _ = fmt.Fprintf(log, "[budget] %s\n", msg)
		return errors.New(msg)
	}
	return nil
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
	// The goal loop builds its own registry, so the delegate has to be wired here
	// too or an unattended goal run - the longest-running thing ycode does - would
	// be the one place that cannot delegate.
	addTaskTool(reg, r, cfg, o)
	allowed := modes.AllowedTools(o.Mode)
	// The goal block is part of the prompt, so it goes through the shared
	// builder: the headless goal run and the TUI goal run should show the
	// model exactly the same thing.
	base := agent.BuildSystem(agent.SystemOptions{
		Mode:      o.Mode,
		Registry:  reg,
		Workdir:   o.Workdir,
		Model:     cfg.ActiveModel,
		Agent:     ag.Name,
		Headless:  true,
		GoalBlock: g.PromptBlock(todoBlock(o.Workdir), ""),
		ZeroLeak:  cfg.ZeroDataLeak,
	})
	hookset := hooks.Load()
	log := o.Stderr
	rounds := modes.Rounds(o.Mode)

	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: base},
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
		// A goal run used to have no fallback at all, so a CI run died on the
		// primary provider's error even with a working fallback configured.
		chain := []agent.Candidate{{Provider: p, Model: model}}
		for _, fb := range r.Fallbacks() {
			if fp, ferr := r.Provider(fb.Provider); ferr == nil {
				chain = append(chain, agent.Candidate{Provider: fp, Model: fb.Model, Label: fb.Provider + "/" + fb.Model})
			}
		}
		workOK := 0
		chainRes := agent.RunChain(ctx, chain, turnMsgs, reg, allowed, hookset, log, &agent.Observer{
			OnTool: func(name, args, result string, err error) {
				status := "ok"
				if err != nil {
					status = "ERR " + err.Error()
				} else if goal.IsWorkTool(name) {
					workOK++
				}
				_, _ = fmt.Fprintf(log, "[goal %d/%d tool %s] %s\n", g.Iter+1, g.MaxIter, name, status)
			},
			OnChunk: func(name, chunk string) {
				_, _ = fmt.Fprintf(log, "[goal %d/%d %s] %s", g.Iter+1, g.MaxIter, name, chunk)
			},
		}, rounds, string(o.Mode), func(label string) {
			_, _ = fmt.Fprintf(log, "[goal %d/%d] retrying on fallback %s\n", g.Iter+1, g.MaxIter, label)
		})
		if chainRes.Text == "" {
			return "", OutcomeUnverified, chainRes.Failure()
		}
		res := chainRes.Text
		if chainRes.Err != nil {
			res += "\n\n(stopped early: " + chainRes.Err.Error() + ")"
		}
		if chainRes.FellBack() {
			_, _ = fmt.Fprintf(log, "[goal %d/%d] fell back to %s\n", g.Iter+1, g.MaxIter, chainRes.Used)
		}
		last = res
		audit.Log("goal_iteration", map[string]any{"iteration": g.Iter + 1, "prompt": g.Text, "answer": res})
		// Check the model's verdict against what actually happened: small
		// models claim GOAL MET on autopilot, and narrate instead of acting.
		g.Reconcile(res, goal.Evidence{
			Calls:          chainRes.Calls,
			SucceededCalls: chainRes.OKs,
			FailedCalls:    chainRes.Failed,
			WorkCalls:      workOK,
			OpenSteps:      openSteps(o.Workdir),
		})
		if g.Rejected() {
			_, _ = fmt.Fprintf(log, "%s\n", g.RejectionNote())
		}
		// Record this iteration's cost before deciding whether to run another.
		//
		// The order matters: checking first would test the spend as it was
		// before this iteration, so the run would always be allowed one more
		// than the limit permits.
		recordUsage(cfg, o.Tracker, r, log)
		if !g.Next() {
			break
		}
		// The budget stop, at the boundary where the reason is visible in the
		// log. Without it the check in runTurn fires one iteration late and
		// reports nothing about why.
		if err := overBudget(cfg, o.Tracker, log); err != nil {
			g.Status = goal.Cancelled
			last += "\n\n(stopped: " + err.Error() + ")"
			_, _ = fmt.Fprintf(log, "goal: %s\n", g.Summary())
			return last, OutcomeSpendLimited, err
		}
		msgs = append(msgs,
			apitypes.Message{Role: apitypes.RoleAssistant, Content: res},
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
