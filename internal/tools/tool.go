package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

func isWindows() bool { return runtime.GOOS == "windows" }

type Tool interface {
	Name() string
	Description() string
	Schema() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is the tool set the agent may call.
//
// It is mutated at runtime: the TUI adds MCP tools and script plugins while a
// turn may be streaming (the turn goroutine reads the registry on every tool
// call and again when building the system prompt). An unsynchronized map there
// is a "fatal error: concurrent map read and map write", which kills the
// process mid-turn and loses the unsaved transcript — so every method is
// guarded. Reads dominate, hence RWMutex.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

func (r *Registry) Add(t Tool) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return
	}
	delete(r.tools, name)
	kept := r.order[:0]
	for _, n := range r.order {
		if n != name {
			kept = append(kept, n)
		}
	}
	r.order = kept
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

func DefaultRegistry(workdir string) *Registry {
	r := NewRegistry()
	r.Add(&ReadTool{})
	r.Add(&ChangesTool{Workdir: workdir})
	r.Add(&GrepTool{})
	r.Add(&GlobTool{})
	r.Add(&WriteTool{Workdir: workdir})
	r.Add(&CreateTool{Workdir: workdir})
	r.Add(&AddTool{Workdir: workdir})
	r.Add(&EditTool{Workdir: workdir})
	r.Add(&RemoveTool{Workdir: workdir})
	r.Add(&SummaryTool{})
	r.Add(NewBashTool(workdir))
	r.Add(&GitTool{Workdir: workdir})
	r.Add(&GitHubTool{Workdir: workdir})
	r.Add(&BrowserTool{})
	r.Add(&TestGenTool{Workdir: workdir})
	r.Add(&SecurityTool{Workdir: workdir})
	r.Add(&TreeTool{})
	r.Add(&TodoTool{Workdir: workdir})
	r.Add(&MemoryTool{})
	r.Add(&PatchTool{Workdir: workdir})
	r.Add(&RunTool{Workdir: workdir})
	r.Add(&DeleteTool{Workdir: workdir})
	r.Add(&ModelsTool{Workdir: workdir})
	r.Add(&DBTool{})
	r.Add(&NotebookTool{Workdir: workdir})
	r.Add(&APITool{})
	r.Add(&VSCodeTool{Workdir: workdir})
	r.Add(&ScaffoldTool{Workdir: workdir})
	return r
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return fmt.Errorf("missing args")
	}
	return json.Unmarshal(raw, v)
}

// --- shared helpers (one copy used by all file tools) ---

// resolve turns a model-supplied path into an absolute-ish one. It does not
// confine anything — see containPath for that.
func resolve(workdir, p string) string {
	if filepath.IsAbs(p) || workdir == "" {
		return p
	}
	return filepath.Join(workdir, p)
}

// containPath confines a model-supplied path to the workdir.
//
// Only delete did this, and its guard could be switched off by the model
// passing force:true — which its own description advertised. write, create,
// add and edit had no check at all, so "../../../../home/u/.config/x" wrote
// outside the project and nothing stopped a write into .git/hooks. Build mode
// also has bash, so this is not a privilege boundary against a determined
// model; it is a rail against the ordinary mistake of a relative path
// escaping, which is exactly the case that loses someone's work silently.
//
// allowEscape overrides it, for the cases that legitimately reach outside:
// an absolute path the user asked for.
func containPath(workdir, raw string, allowEscape bool) (string, error) {
	p := resolve(workdir, raw)
	clean := filepath.Clean(p)
	if workdir == "" || allowEscape {
		return clean, nil
	}
	wd, err := filepath.Abs(workdir)
	if err != nil {
		return clean, nil // cannot judge; do not block
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return clean, nil
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return "", fmt.Errorf("refusing path outside the workdir: %s", raw)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to touch %s: outside the workdir (%s) — use a path inside the project", raw, workdir)
	}
	return abs, nil
}

// guardGitKeep blocks writes into .git, where a stray file can turn into a
// hook the user never wrote.
func guardGitKeep(p string) error {
	for _, seg := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if seg == ".git" {
			return fmt.Errorf("refusing to write inside .git: %s", p)
		}
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	out = append(out, cur)
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type fileEntry string

func (f fileEntry) Name() string               { return filepath.Base(string(f)) }
func (f fileEntry) IsDir() bool                { return false }
func (f fileEntry) Type() os.FileMode          { return 0 }
func (f fileEntry) Info() (os.FileInfo, error) { return os.Stat(string(f)) }

// diffBlock renders old→new line diffs (changed lines only + hunk headers),
// capped at maxLines. Used inside ```diff fences by the chat renderer.
func diffBlock(oldLines, newLines []string, maxLines int) string {
	type op struct {
		kind byte // '-', '+'
		text string
	}
	// LCS table, capped to keep it cheap.
	n, m := len(oldLines), len(newLines)
	if n > 600 || m > 600 {
		return fmt.Sprintf("@@ large change: %d → %d lines @@\n", n, m)
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []op
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && oldLines[i] == newLines[j]:
			i++
			j++
		case j < m && (i >= n || dp[i][j+1] >= dp[i+1][j]):
			ops = append(ops, op{'+', newLines[j]})
			j++
		default:
			ops = append(ops, op{'-', oldLines[i]})
			i++
		}
	}
	if len(ops) == 0 {
		return "(no changes)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", n, m)
	shown := 0
	for _, o := range ops {
		if shown >= maxLines {
			fmt.Fprintf(&b, "… (%d more changed lines)\n", len(ops)-shown)
			break
		}
		b.WriteString(string(o.kind) + " " + o.text + "\n")
		shown++
	}
	return b.String()
}
