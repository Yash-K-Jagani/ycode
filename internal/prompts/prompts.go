package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

var builtins = map[string]string{
	"review":   "Review the diff below. Output: summary, per-file findings with file:line, concrete fixes. Be concise.\n\n```diff\n{{input}}\n```",
	"explain":  "Explain the following code clearly: what it does, key edge cases, and risks.\n\n```\n{{input}}\n```",
	"commit":   "Write a conventional-commit message (type(scope): subject, body) for the diff below. Output only the message.\n\n```diff\n{{input}}\n```",
	"testplan": "Propose a test plan for the change below: cases, files to add tests in, edge cases.\n\n```diff\n{{input}}\n```",
}

type Manager struct {
	dir string
}

func defaultDir() string { return filepath.Join(config.Dir(), "prompts") }

func NewManager() *Manager { return NewManagerAt(defaultDir()) }

func NewManagerAt(dir string) *Manager { return &Manager{dir: dir} }

func (m *Manager) List() []string {
	set := map[string]bool{}
	for n := range builtins {
		set[n+" (builtin)"] = true
	}
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			set[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) Show(name string) (string, error) {
	if p := filepath.Join(m.dir, name+".md"); fileExists(p) {
		b, err := os.ReadFile(p)
		return string(b), err
	}
	if b, ok := builtins[name]; ok {
		return b, nil
	}
	return "", fmt.Errorf("prompt %q not found", name)
}

// Save writes a new version (keeps numbered snapshots under versions/).
func (m *Manager) Save(name, body string) error {
	_ = os.MkdirAll(m.dir, 0o755)
	_ = os.MkdirAll(filepath.Join(m.dir, "versions"), 0o755)
	p := filepath.Join(m.dir, name+".md")
	if fileExists(p) {
		old, _ := os.ReadFile(p)
		ts := time.Now().Format("20060102-150405")
		_ = os.WriteFile(filepath.Join(m.dir, "versions", fmt.Sprintf("%s.%s.md", name, ts)), old, 0o644)
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

func (m *Manager) Versions(name string) []string {
	entries, _ := os.ReadDir(filepath.Join(m.dir, "versions"))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), name+".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Render substitutes {{input}} in the template.
func Render(tmpl, input string) string {
	return strings.ReplaceAll(tmpl, "{{input}}", input)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
