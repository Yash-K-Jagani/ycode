package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type Manager struct {
	mu      sync.Mutex
	cfgPath string
	servers map[string]ServerConfig
	clients map[string]*Client
}

func filePath() string { return filepath.Join(config.Dir(), "mcp.json") }

func NewManager() *Manager { return NewManagerAt(filePath()) }

func NewManagerAt(path string) *Manager {
	m := &Manager{cfgPath: path, servers: map[string]ServerConfig{}, clients: map[string]*Client{}}
	data, _ := os.ReadFile(m.cfgPath)
	var v struct {
		Servers map[string]ServerConfig `json:"servers"`
	}
	if json.Unmarshal(data, &v) == nil && v.Servers != nil {
		m.servers = v.Servers
	}
	return m
}

func (m *Manager) save() error {
	data, _ := json.MarshalIndent(map[string]any{"servers": m.servers}, "", "  ")
	_ = os.MkdirAll(config.Dir(), 0o755)
	return os.WriteFile(m.cfgPath, data, 0o644)
}

func (m *Manager) Add(name string, sc ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.servers[name]; ok {
		return fmt.Errorf("server %q already exists (remove first)", name)
	}
	m.servers[name] = sc
	return m.save()
}

func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[name]; ok {
		c.Close()
		delete(m.clients, name)
	}
	if _, ok := m.servers[name]; !ok {
		return fmt.Errorf("unknown server %q", name)
	}
	delete(m.servers, name)
	return m.save()
}

func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.servers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) ensure(ctx context.Context, name string) (*Client, error) {
	m.mu.Lock()
	if c, ok := m.clients[name]; ok {
		m.mu.Unlock()
		return c, nil
	}
	sc, ok := m.servers[name]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown server %q", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := Start(sc.Command, sc.Args, sc.Env)
	if err != nil {
		return nil, fmt.Errorf("start %q: %w", name, err)
	}
	if err := c.Initialize(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("init %q: %w", name, err)
	}
	m.mu.Lock()
	m.clients[name] = c
	m.mu.Unlock()
	return c, nil
}

type adapter struct {
	server string
	def    ToolDef
	call   func(ctx context.Context, name string, args json.RawMessage) (string, error)
}

func (a *adapter) Name() string { return "mcp__" + a.server + "__" + a.def.Name }
func (a *adapter) Description() string {
	return "[mcp:" + a.server + "] " + a.def.Description
}
func (a *adapter) Schema() string {
	b, _ := json.Marshal(a.def.InputSchema)
	if len(b) == 0 || string(b) == "null" {
		return `{"type":"object"}`
	}
	return string(b)
}
func (a *adapter) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if tools.IsReadOnly(ctx) {
		return "", fmt.Errorf("mcp tools are blocked in read-only mode")
	}
	return a.call(ctx, a.def.Name, args)
}

// Tools starts configured servers on demand and returns their tools as ycode tools.
func (m *Manager) Tools(ctx context.Context) ([]tools.Tool, error) {
	var out []tools.Tool
	var errs []string
	for _, name := range m.Names() {
		c, err := m.ensure(ctx, name)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		defs, err := c.ListTools(ctx)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		for _, d := range defs {
			d := d
			out = append(out, &adapter{server: name, def: d, call: c.CallTool})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("mcp: %s", joinErrs(errs))
	}
	return out, nil
}

func joinErrs(errs []string) string {
	s := ""
	for i, e := range errs {
		if i > 0 {
			s += "; "
		}
		s += e
	}
	return s
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		c.Close()
	}
	m.clients = map[string]*Client{}
}
