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

	"github.com/Yash-K-Jagani/ycode/internal/textutil"
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
	// concurrent and isolatable record the two capability flags per instance,
	// rather than looking them up by name in the catalog at the point of use.
	//
	// The catalog answers for built-ins, but a plugin or a test cannot be
	// classified by name lookup, and a package-level IsConcurrent(name) had no
	// way to ask the instance that actually holds the tool. Storing them here
	// means a caller can declare a capability for a tool this package has never
	// heard of, and that the agent loop is asking the right object.
	concurrent map[string]bool
	isolatable map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{
		tools:      map[string]Tool{},
		concurrent: map[string]bool{},
		isolatable: map[string]bool{},
	}
}

// Add registers a tool. Its capabilities come from the catalog when it is a
// built-in; an unknown tool is local and not concurrent, which is the safe
// reading of a program this package has not inspected.
func (r *Registry) Add(t Tool) {
	if t == nil {
		return
	}
	r.AddWith(t, IsConcurrent(t.Name()), IsIsolatable(t.Name(), t))
}

// AddWith registers a tool with explicit capabilities, overriding the catalog.
//
// This is how a plugin declares that its tool only reads, or a test registers
// an instrumented stand-in for a built-in. The override is deliberate rather
// than a fallback: the caller has the tool in hand and knows what it does,
// whereas the catalog only knows the name.
func (r *Registry) AddWith(t Tool, concurrent, isolatable bool) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
	r.concurrent[t.Name()] = concurrent
	r.isolatable[t.Name()] = isolatable
}

// Concurrent reports whether a registered tool only reads, and so may run at
// the same time as other concurrent tools.
func (r *Registry) Concurrent(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.concurrent[name]
}

// Isolatable reports whether a registered tool may be handed to a subagent.
func (r *Registry) Isolatable(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.isolatable[name]; ok {
		return v
	}
	return IsIsolatable(name, nil)
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
	for _, e := range catalog {
		r.Add(e.build(workdir))
	}
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
		return textutil.Truncate(s, n)
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
