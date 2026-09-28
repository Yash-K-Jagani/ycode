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
// This table is the single place those numbers live. A tool whose own internal
// timeout is shorter still wins, because it derives from the same context.
const defaultToolTimeout = 2 * time.Minute

var toolTimeouts = map[string]time.Duration{
	"bash":     60 * time.Second,
	"db":       30 * time.Second,
	"git":      60 * time.Second,
	"changes":  30 * time.Second,
	"api":      30 * time.Second,
	"browser":  30 * time.Second,
	"patch":    60 * time.Second,
	"security": 60 * time.Second,
	"run":      3 * time.Minute,
	"testgen":  5 * time.Minute,
	"notebook": 5 * time.Minute,
	"github":   5 * time.Minute,
	"scaffold": 10 * time.Minute,
	"vscode":   30 * time.Second,
}

// TimeoutFor is how long a tool call may run.
func TimeoutFor(name string) time.Duration {
	if d, ok := toolTimeouts[name]; ok {
		return d
	}
	// MCP and plugin tools are arbitrary shell commands.
	if strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "plugin__") {
		return 5 * time.Minute
	}
	return defaultToolTimeout
}
