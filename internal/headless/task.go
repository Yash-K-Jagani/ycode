package headless

import (
	"fmt"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/subagent"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// addTaskTool registers the subagent delegate for a headless run.
//
// It was missing here, and flagging that was the point: without it an unattended
// `ycode run --mode build` or a CI goal run could not delegate a broad read-only
// question, so the feature existed only for the one entry point a person was
// sitting in front of.
//
// There is no program to report progress to, so progress goes to the log, which
// is where someone watching a CI job is looking anyway. Milestones only: a
// subagent's internal output is not the parent's business, and dumping it would
// fill the log with the noise this feature exists to avoid.
//
// Not offered at all under zero-data-leak. The only delegable tool that reaches
// the network is browser, so a local-only user would get a confusing refusal at
// call time instead of simply not having the feature.
//
// Removed rather than left unwired: task is in the tool catalog, so merely
// skipping the wiring leaves the catalog's stub in place, and its error is "task
// is registered but not available in this context" - which tells a local-only
// user the feature is broken rather than that it does not apply to them.
// Silently dropping a tool is normally wrong; here the alternative is worse.
func addTaskTool(reg *tools.Registry, r *router.Router, cfg config.Config, o Options) {
	if reg == nil || r == nil {
		return
	}
	if cfg.ZeroDataLeak {
		reg.Remove(tools.TaskTool{}.Name())
		return
	}
	reg.AddWith(&tools.TaskTool{
		Delegate: subagent.Delegate(subagent.ToolOptions{
			Resolve: func() (providers.Provider, string) {
				p, model, err := r.Active()
				if err != nil {
					return nil, ""
				}
				return p, model
			},
			Registry: reg,
			Workdir:  o.Workdir,
			Mode:     string(o.Mode),
			Agent:    "explorer",
			Timeout:  3 * time.Minute,
			OnEvent: func(kind, text string) {
				switch kind {
				case "start", "done", "error", "agent":
					_, _ = fmt.Fprintf(o.Stderr, "[subagent] %s\n", text)
				}
			},
		}),
	}, false, false)
}
