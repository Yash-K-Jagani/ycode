package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/providers/gemini"
	"github.com/Yash-K-Jagani/ycode/internal/providers/groq"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openrouter"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type connectStep int

const (
	cStepProvider connectStep = iota
	cStepKey
	cStepFetch
	cStepModels
)

type connectProv struct {
	ID     string
	Label  string
	KeyEnv string
	IsHost bool
}

func connectProviders() []connectProv {
	return []connectProv{
		{ID: "ollama", Label: "Ollama (local, no key)", IsHost: true},
		{ID: "gemini", Label: "Gemini", KeyEnv: "GEMINI_API_KEY"},
		{ID: "openrouter", Label: "OpenRouter", KeyEnv: "OPENROUTER_API_KEY"},
		{ID: "groq", Label: "Groq", KeyEnv: "GROQ_API_KEY"},
	}
}

type connectResult struct {
	Provider string
	Model    string
	Key      string
	KeyEnv   string
	Host     string
}

type fetchModelsMsg struct {
	models []apitypes.ModelInfo
	note   string
}

type connectFlow struct {
	step     connectStep
	provIdx  int
	provs    []connectProv
	keyInput textinput.Model
	key      string
	models   []apitypes.ModelInfo
	modelIdx int
	note     string
	err      string
	done     bool
	result   connectResult
}

func newConnect() *connectFlow {
	ti := textinput.New()
	ti.Placeholder = "paste API key"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Focus()
	return &connectFlow{step: cStepProvider, provs: connectProviders(), keyInput: ti}
}

func maskKey(s string) string {
	if len(s) <= 8 {
		return "•••"
	}
	return s[:2] + "•••" + s[len(s)-4:]
}

// fetchModelsCmd verifies the credential and lists models (registry fallback).
func (m *Model) fetchModelsCmd(prov connectProv, cred string) tea.Cmd {
	host := m.cfg.OllamaHost
	if prov.IsHost && cred != "" {
		host = cred
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var p interface {
			ListModels(ctx context.Context) ([]apitypes.ModelInfo, error)
		}
		switch prov.ID {
		case "ollama":
			p = ollama.New(host)
		case "gemini":
			p = gemini.New(cred)
		case "openrouter":
			p = openrouter.New(cred)
		case "groq":
			p = groq.New(cred)
		}
		if ms, err := p.ListModels(ctx); err == nil && len(ms) > 0 {
			return fetchModelsMsg{models: ms}
		} else if err != nil && prov.ID == "ollama" {
			return fetchModelsMsg{note: "unreachable: " + err.Error()}
		}
		var fb []apitypes.ModelInfo
		for _, c := range registry.Catalog() {
			if c.Provider == prov.ID {
				fb = append(fb, c)
			}
		}
		if len(fb) == 0 {
			return fetchModelsMsg{note: "no models found — check the key and retry"}
		}
		return fetchModelsMsg{models: fb, note: "catalog defaults (live list unavailable)"}
	}
}

// updateConnect routes messages to the modal. Returns a cmd.
func (m *Model) updateConnect(msg tea.Msg) tea.Cmd {
	c := m.conn
	switch msg := msg.(type) {
	case fetchModelsMsg:
		if len(msg.models) == 0 {
			c.err = msg.note
			if c.err == "" {
				c.err = "could not list models"
			}
			c.step = cStepKey
			c.keyInput.Focus()
			return textinput.Blink
		}
		c.models = msg.models
		c.note = msg.note
		c.modelIdx = 0
		c.err = ""
		c.step = cStepModels
		return nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if c.step == cStepProvider {
				m.conn = nil
				return nil
			}
			c.err = ""
			c.step--
			if c.step == cStepKey {
				c.keyInput.Focus()
				return textinput.Blink
			}
			return nil
		case "up":
			if c.step == cStepProvider && c.provIdx > 0 {
				c.provIdx--
			} else if c.step == cStepModels && c.modelIdx > 0 {
				c.modelIdx--
			}
			return nil
		case "down":
			if c.step == cStepProvider && c.provIdx < len(c.provs)-1 {
				c.provIdx++
			} else if c.step == cStepModels && c.modelIdx < len(c.models)-1 {
				c.modelIdx++
			}
			return nil
		case "enter":
			return m.connectAdvance()
		}
	}
	if c.step == cStepKey {
		var cmd tea.Cmd
		c.keyInput, cmd = c.keyInput.Update(msg)
		return cmd
	}
	return nil
}

func (m *Model) connectAdvance() tea.Cmd {
	c := m.conn
	switch c.step {
	case cStepProvider:
		p := c.provs[c.provIdx]
		c.key = ""
		c.err = ""
		c.keyInput.SetValue("")
		if p.IsHost {
			c.keyInput.Placeholder = "Ollama host (empty = " + m.cfg.OllamaHost + ")"
			c.keyInput.EchoMode = textinput.EchoNormal
		} else {
			c.keyInput.Placeholder = "paste " + p.Label + " API key"
			c.keyInput.EchoMode = textinput.EchoPassword
		}
		c.step = cStepKey
		c.keyInput.Focus()
		return textinput.Blink
	case cStepKey:
		p := c.provs[c.provIdx]
		cred := strings.TrimSpace(c.keyInput.Value())
		if cred == "" && !p.IsHost {
			c.err = "paste a key first (esc to go back)"
			return nil
		}
		c.key = cred
		c.err = ""
		c.step = cStepFetch
		return m.fetchModelsCmd(p, cred)
	case cStepModels:
		if len(c.models) == 0 {
			return nil
		}
		p := c.provs[c.provIdx]
		chosen := c.models[c.modelIdx%len(c.models)]
		c.result = connectResult{Provider: p.ID, Model: chosen.ID, Key: c.key, KeyEnv: p.KeyEnv, Host: c.key}
		c.done = true
		return nil
	}
	return nil
}

func (m *Model) applyConnect(r connectResult) string {
	if r.Provider == "ollama" {
		if r.Host != "" {
			m.cfg.OllamaHost = r.Host
		}
	} else if r.KeyEnv != "" && r.Key != "" {
		_ = os.Setenv(r.KeyEnv, r.Key)
		switch r.Provider {
		case "gemini":
			m.cfg.GeminiAPIKey = r.Key
		case "openrouter":
			m.cfg.OpenRouterKey = r.Key
		case "groq":
			m.cfg.GroqKey = r.Key
		}
	}
	out := applyModel(m, r.Provider, r.Model)
	if r.Provider == "ollama" {
		return out + " (host " + m.cfg.OllamaHost + ")"
	}
	return out + " (key " + maskKey(r.Key) + " active for this session - persist with: export " + r.KeyEnv + "=...)"
}

func (c *connectFlow) view(width int, accent lipgloss.Color) string {
	if width < 30 {
		width = 30
	}
	if width > 64 {
		width = 64
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Connect provider")
	steps := []string{"provider", "key", "models"}
	var crumbs []string
	for i, s := range steps {
		if connectStep(i) == c.step || (c.step == cStepFetch && i == 2) {
			crumbs = append(crumbs, lipgloss.NewStyle().Bold(true).Foreground(accent).Render(s))
		} else {
			crumbs = append(crumbs, lipgloss.NewStyle().Faint(true).Render(s))
		}
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	b.WriteString(strings.Join(crumbs, " › ") + "\n\n")
	dim := lipgloss.NewStyle().Faint(true)
	sel := lipgloss.NewStyle().Bold(true).Foreground(accent)
	switch c.step {
	case cStepProvider:
		for i, p := range c.provs {
			mark := "  "
			if i == c.provIdx {
				mark = sel.Render("▸ ")
			}
			name := p.Label
			if i == c.provIdx {
				name = sel.Render(p.Label)
			}
			fmt.Fprintf(&b, "%s%s\n", mark, name)
		}
	case cStepKey:
		p := c.provs[c.provIdx]
		b.WriteString(dim.Render(p.Label) + "\n")
		b.WriteString(c.keyInput.View() + "\n")
		if c.err != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Render(c.err) + "\n")
		}
	case cStepFetch:
		b.WriteString(dim.Render("contacting provider…") + "\n")
	case cStepModels:
		if c.note != "" {
			b.WriteString(dim.Render(c.note) + "\n")
		}
		start, end := window(c.modelIdx, len(c.models), 8)
		for i := start; i < end; i++ {
			name := c.models[i].ID
			if len(name) > width-10 {
				name = name[:width-10] + "…"
			}
			if i == c.modelIdx {
				fmt.Fprintf(&b, "%s\n", sel.Render("▸ "+name))
			} else {
				fmt.Fprintf(&b, "  %s\n", name)
			}
		}
	}
	b.WriteString("\n" + dim.Render("↑↓ move · enter select · esc back"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Width(width).Padding(0, 1)
	return box.Render(b.String())
}

func window(sel, n, size int) (int, int) {
	if n <= size {
		return 0, n
	}
	start := sel - size + 1
	if start < 0 {
		start = 0
	}
	if start+size > n {
		start = n - size
	}
	return start, start + size
}
