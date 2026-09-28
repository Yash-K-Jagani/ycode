package tools

import "strings"

// Network policy.
//
// Zero-Data-Leak mode is a guardrail, and until now it was enforced in three
// places (browser, git, github) out of twenty-eight tools, while the UI banner
// claimed "local only" and told the model nothing could reach the network. Tools
// that obviously could — api, the database drivers, the notebook runner,
// scaffolding, every MCP and plugin tool — sailed straight through, so the
// promise was false and, worse, the model was misinformed.
//
// The classification now lives here, in one place, and is enforced centrally in
// the agent loop before any tool runs. A new tool cannot forget to declare
// itself: the loop asks this table, and a name that is not listed is assumed
// local.
//
// What this is not: a sandbox. bash and run execute arbitrary commands, so a
// determined (or careless) `bash curl ...` still leaves the machine. Blocking
// every tool that could reach a socket would mean blocking all real work, so
// the direct channels are closed and the residual risk is documented in the
// banner and in docs/security.md.
var networkTools = map[string]bool{
	"api":      true, // arbitrary HTTP methods and bodies
	"browser":  true, // fetches and extracts arbitrary URLs
	"github":   true, // GitHub API
	"git":      true, // fetch/push
	"db":       true, // postgres/mysql/mongo can be remote hosts
	"notebook": true, // executes arbitrary code
	"scaffold": true, // npm/pip installs from remote registries
	"vscode":   true, // shells out to the editor
	"memory":   false,
	"run":      false, // executes code: documented residual risk, see above
	"bash":     false, // executes commands: documented residual risk, see above
	"testgen":  false, // runs the project's own tests
	"security": false,
	"models":   false,
	"todo":     false,
	"summary":  false,
	"changes":  false,
	"read":     false,
	"grep":     false,
	"glob":     false,
	"tree":     false,
	"write":    false,
	"create":   false,
	"add":      false,
	"edit":     false,
	"remove":   false,
	"delete":   false,
	"patch":    false,
}

// ReachesNetwork reports whether a tool can send data off the machine.
//
// MCP (mcp__server__tool) and script-plugin (plugin__name) tools are opaque
// shell commands, so they are treated as network-capable.
func ReachesNetwork(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "plugin__") {
		return true
	}
	// An unlisted tool is assumed local: that way adding a new tool is safe by
	// default, and only tools that genuinely reach out need declaring.
	return networkTools[name]
}

// NetworkTools lists the declared network-capable tools, for diagnostics.
func NetworkTools() []string {
	out := make([]string, 0, len(networkTools))
	for n, yes := range networkTools {
		if yes {
			out = append(out, n)
		}
	}
	return out
}

// BlockNetworkReason explains why a tool is refused in Zero-Data-Leak mode.
func BlockNetworkReason(name string) error {
	return &netBlockedError{tool: name}
}

type netBlockedError struct{ tool string }

func (e *netBlockedError) Error() string {
	return "tool " + e.tool + " can reach the network and is blocked in zero-data-leak mode " +
		"(local only). Set zero_data_leak: false in ~/.ycode/config.yaml to allow it."
}
