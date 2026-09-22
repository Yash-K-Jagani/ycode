package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
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
	r.Add(&EditTool{Workdir: workdir})
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
