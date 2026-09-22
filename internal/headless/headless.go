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
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Options struct {
	Mode    modes.Mode
	Agent   string
	Workdir string
	Timeout time.Duration
	Stderr  io.Writer
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

// Run executes a single headless turn: system prompt + optional tool loop.
func Run(ctx context.Context, cfg config.Config, prompt string, o Options) (string, error) {
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
	if o.Mode == modes.Build || o.Mode == modes.Plan {
		sys += "\nRepo tree (" + o.Workdir + ") — real paths, use them directly:\n" + yctx.Tree(o.Workdir, 150, 4000) +
			"NEVER ask the user for paths or locations. If a file is named without a path, find it with glob/grep yourself."
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
	res, err := agent.Run(ctx, p, model, msgs, reg, allowed, hookset, log, func(name, args, result string, err error) {
		status := "ok"
		if err != nil {
			status = "ERR " + err.Error()
		}
		_, _ = fmt.Fprintf(log, "[tool %s] %s\n", name, status)
	})
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
		res, err = agent.Run(ctx, fp, fbs[0].Model, msgs, reg, allowed, hookset, io.Discard, nil)
		if err != nil && res.Text == "" {
			return "", err
		}
		res.Text += "\n\n(fell back to " + fbs[0].Provider + "/" + fbs[0].Model + ")"
	}
	hookset.Fire(ctx, hooks.OnResponse, map[string]string{"mode": string(o.Mode)})
	audit.Log("headless_turn", map[string]any{"mode": string(o.Mode), "prompt": prompt, "answer": res.Text})
	return res.Text, nil
}
