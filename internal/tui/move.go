package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// moveTo changes the directory the session works in.
//
// Sessions are stored centrally under ~/.ycode, keyed by id, not in the working
// directory. So this does not move any files: it changes which tree the tools
// read and write, and everything already said in the conversation still refers to
// the old one. Saying so is the difference between a pleasant surprise and a
// bug report.
//
// The registry is rebuilt because tools capture the workdir when they are
// constructed. Changing m.workdir alone would leave read, write and bash pointing
// at the old tree while the status bar claimed the new one - the worst possible
// combination, because it looks correct until a file is written in the wrong
// place.
func (m *Model) moveTo(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("provide a directory: /move <path>")
	}
	// Expanded before resolution so ~ works, which is how anyone actually types a
	// path.
	if strings.HasPrefix(target, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~: %w", err)
		}
		target = filepath.Join(home, strings.TrimPrefix(target, "~"))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", target, err)
	}

	// Existence is checked rather than assumed. Creating a directory because
	// someone typed a path with a typo in it produces a stray empty folder and
	// a session that then reports no files, which is harder to diagnose than a
	// clear refusal.
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no such directory: %s\n  (create it first, or /init a project there)", abs)
		}
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is a file, not a directory", abs)
	}
	// Writability is checked because most modes can write, and discovering it on
	// the first write is a much worse moment than being told now. A read-only
	// mode still works, so this is reported rather than fatal.
	writable := true
	if probe, perr := os.CreateTemp(abs, ".ycode-probe-*"); perr != nil {
		writable = false
	} else {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	}

	old := m.workdir
	if sameDir(old, abs) {
		return fmt.Sprintf("already working in %s", abs), nil
	}

	m.workdir = abs
	// Rebuilt, not mutated: the old registry's tools hold the old path.
	m.toolreg = tools.DefaultRegistry(abs)
	m.reRegisterDynamicTools()
	// The sidebar caches the task list, which is read from the old tree.
	m.invalidateSidebar()
	// The todo block is rendered into the system prompt from the old tree, and a
	// goal run in progress has its own copy.
	if m.goal != nil {
		m.goal = nil
	}

	note := fmt.Sprintf("working directory is now %s", abs)
	if old != "" {
		note += fmt.Sprintf("\n  (was %s)", old)
	}
	// Said plainly because the conversation does not change: every path already
	// discussed, and every file already read, refers to the old tree.
	note += "\n  paths from earlier in this session still refer to the previous directory"
	if !writable {
		note += "\n  note: this directory is not writable, so build and goal modes will fail on any write"
	}
	return note, nil
}

// sameDir compares two paths after cleaning them, case-insensitively on Windows
// and macOS where the filesystem is.
//
// Case matters on Linux, so folding it there would treat two different
// directories as one and quietly skip a legitimate move.
func sameDir(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if os.PathSeparator == '\\' || strings.EqualFold(ca, cb) {
		if os.PathSeparator == '\\' {
			return strings.EqualFold(ca, cb)
		}
	}
	return ca == cb
}

// reRegisterDynamicTools restores the tools that are added after the catalog:
// plugins and MCP servers. Rebuilding the registry drops them, and a session that
// lost its MCP tools on a /move would be a confusing regression.
func (m *Model) reRegisterDynamicTools() {
	m.registerPluginTools()
	if m.mcpMgr == nil {
		return
	}
	// Best effort and silent: MCP servers may be down, and a /move should not
	// fail because one is. The tools come back on the next turn, which refreshes
	// them anyway.
	if live, err := m.mcpMgr.Tools(context.Background()); err == nil {
		for _, t := range live {
			m.toolreg.Add(t)
		}
	}
}
