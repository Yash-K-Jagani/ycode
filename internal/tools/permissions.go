package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

// Verdicts for a gated tool call.
type Verdict int

const (
	DenyOnce Verdict = iota
	AllowOnce
	AllowAlways
	DenyAlways
)

// NeedsApproval reports whether a call needs a user verdict.
// Read-only actions never prompt; everything else mutating does.
func NeedsApproval(name, argsJSON string) bool {
	var args struct {
		Action string `json:"action"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	switch name {
	case "read", "grep", "glob", "tree", "todo", "security", "browser", "memory":
		return false
	case "notebook":
		return args.Action == "execute"
	case "api":
		m := strings.ToUpper(args.Method)
		return m != "" && m != "GET" && m != "HEAD" && m != "OPTIONS"
	case "git":
		switch args.Action {
		case "status", "diff", "log", "branch", "show", "remote", "":
			return false
		}
		return true
	case "github":
		switch args.Action {
		case "pr_list", "pr_view", "":
			return false
		}
		return true
	case "models":
		return args.Action == "import"
	case "vscode":
		return args.Action == "open"
	case "write", "create", "add", "edit", "remove", "delete", "bash", "patch", "run", "testgen", "scaffold":
		return true
	default:
		// MCP + script plugins + unknown: prompt (fail closed).
		return strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "plugin__")
	}
}

type permFile struct {
	Always []string `json:"always"`
	Never  []string `json:"never"`
}

// PermStore remembers always/never decisions.
type PermStore struct {
	mu     sync.Mutex
	path   string
	always map[string]bool
	never  map[string]bool
}

func permPath() string { return filepath.Join(config.Dir(), "permissions.json") }

func NewPermStore() *PermStore { return NewPermStoreAt(permPath()) }

func NewPermStoreAt(path string) *PermStore {
	p := &PermStore{path: path, always: map[string]bool{}, never: map[string]bool{}}
	data, _ := os.ReadFile(path)
	var f permFile
	if json.Unmarshal(data, &f) == nil {
		for _, n := range f.Always {
			p.always[n] = true
		}
		for _, n := range f.Never {
			p.never[n] = true
		}
	}
	return p
}

func (p *PermStore) save() {
	var f permFile
	for n := range p.always {
		f.Always = append(f.Always, n)
	}
	for n := range p.never {
		f.Never = append(f.Never, n)
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	_ = os.MkdirAll(filepath.Dir(p.path), 0o755)
	_ = os.WriteFile(p.path, data, 0o644)
}

// Check returns allow/deny without prompting when remembered.
func (p *PermStore) Check(name string) (Verdict, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.never[name] {
		return DenyOnce, true
	}
	if p.always[name] {
		return AllowOnce, true
	}
	return DenyOnce, false
}

func (p *PermStore) Remember(name string, v Verdict) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch v {
	case AllowAlways:
		p.always[name] = true
		delete(p.never, name)
	case DenyAlways:
		p.never[name] = true
		delete(p.always, name)
	}
	p.save()
}

// List returns remembered decisions as "tool: always|never" lines.
func (p *PermStore) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for n := range p.always {
		out = append(out, n+": always")
	}
	for n := range p.never {
		out = append(out, n+": never")
	}
	sort.Strings(out)
	return out
}

// Clear forgets one tool (or everything with empty name).
func (p *PermStore) Clear(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" {
		p.always = map[string]bool{}
		p.never = map[string]bool{}
	} else {
		delete(p.always, name)
		delete(p.never, name)
	}
	p.save()
}

// ApprovalReq is sent to the UI layer for a verdict.
type ApprovalReq struct {
	Tool string
	Args string
	Done chan Verdict
}

type gateKey struct{}

// Gate carries an approval channel + remembered permissions through ctx.
type Gate struct {
	Store *PermStore
	Ask   chan *ApprovalReq
}

func WithGate(ctx context.Context, g *Gate) context.Context {
	return context.WithValue(ctx, gateKey{}, g)
}

func gateFrom(ctx context.Context) *Gate {
	g, _ := ctx.Value(gateKey{}).(*Gate)
	return g
}

// Approved returns true when a mutating call may proceed (prompting via Ask).
func Approved(ctx context.Context, tool, args string) bool {
	g := gateFrom(ctx)
	if g == nil || g.Store == nil {
		return true
	}
	if v, ok := g.Store.Check(tool); ok {
		return v == AllowOnce
	}
	if g.Ask == nil {
		return true
	}
	req := &ApprovalReq{Tool: tool, Args: args, Done: make(chan Verdict, 1)}
	select {
	case g.Ask <- req:
	case <-ctx.Done():
		return false
	}
	select {
	case v := <-req.Done:
		switch v {
		case AllowAlways, DenyAlways:
			g.Store.Remember(tool, v)
			return v == AllowAlways
		default:
			return v == AllowOnce
		}
	case <-ctx.Done():
		return false
	}
}
