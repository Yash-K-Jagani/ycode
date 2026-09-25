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
)

func isWindows() bool { return runtime.GOOS == "windows" }

type Tool interface {
	Name() string
	Description() string
	Schema() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

func (r *Registry) Add(t Tool) {
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.tools[name]; return t, ok }

func (r *Registry) Remove(name string) {
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
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

func DefaultRegistry(workdir string) *Registry {
	r := NewRegistry()
	r.Add(&ReadTool{})
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

func resolve(workdir, p string) string {
	if filepath.IsAbs(p) || workdir == "" {
		return p
	}
	return filepath.Join(workdir, p)
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
