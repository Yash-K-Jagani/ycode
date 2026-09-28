package tools

import "strings"

// Network policy.
//
// Zero-Data-Leak mode is a guardrail. It used to be enforced in three places
// (browser, git, github) out of twenty-eight tools, while the UI banner claimed
// "local only" and told the model nothing could reach the network. Tools that
// obviously could — api, the database drivers, the notebook runner,
// scaffolding, every MCP and plugin tool — sailed straight through, so the
// promise was false and, worse, the model was misinformed.
//
// The classification now lives in the tool catalog, in the same row that
// registers the tool, and is enforced centrally in the agent loop before any
// tool runs. A new tool cannot forget to declare itself: there is nowhere else
// to declare it.
//
// What this is not: a sandbox. bash and run execute arbitrary commands, so a
// determined (or careless) `bash curl ...` still leaves the machine. Blocking
// every tool that could reach a socket would mean blocking all real work, so
// the direct channels are closed and the residual risk is recorded in the
// catalog row, the banner, and docs/security.md.

// ReachesNetwork reports whether a tool can send data off the machine.
//
// MCP (mcp__server__tool) and script-plugin (plugin__name) tools are opaque
// shell commands, so they are treated as network-capable.
func ReachesNetwork(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "plugin__") {
		return true
	}
	if e := entryFor(name); e != nil {
		return e.network
	}
	// A name with no catalog row is not a built-in tool. An unrecognised name
	// is assumed local, so a newly registered external tool is usable by
	// default and only has to be declared if it genuinely reaches out.
	return false
}

// NetworkTools lists the built-in tools that can reach the network, for the
// banner and for `doctor`. MCP and plugin tools are not listed because they are
// only known at runtime; they are covered by the prefix rule in ReachesNetwork.
func NetworkTools() []string {
	var out []string
	for _, e := range catalog {
		if e.network {
			out = append(out, e.name)
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
