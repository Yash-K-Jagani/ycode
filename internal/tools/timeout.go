package tools

import (
	"strings"
	"time"
)

// Per-tool timeouts.
//
// The agent loop used to cap every tool call at 90s. Several tools declare
// longer budgets of their own — testgen 5m, notebook execute 5m, github clone
// 5m, scaffold 10m — so a `go build ./...` or an `npm install` that took two
// minutes was killed at 90s and the model was told "context deadline exceeded",
// usually giving up on work that was nearly done. The loop's cap has to be at
// least as generous as the tool's, or it is the cap that binds.
//
// The budgets live in the tool catalog beside the tool itself, so a tool
// cannot be registered without one. A tool whose own internal timeout is
// shorter still wins, because it derives from the same context.
const defaultToolTimeout = 2 * time.Minute

// TimeoutFor is how long a tool call may run.
func TimeoutFor(name string) time.Duration {
	name = strings.ToLower(name)
	if e := entryFor(name); e != nil {
		return e.timeout
	}
	// MCP and plugin tools are arbitrary shell commands, so they get the
	// generous external budget rather than the default.
	if strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "plugin__") {
		return external
	}
	return defaultToolTimeout
}
