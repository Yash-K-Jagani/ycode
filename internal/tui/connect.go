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

	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type connectStep int

const (
	cStepProvider connectStep = iota
	cStepKey
	cStepFetch
	cStepModels
	cStepSave
)

type connectProv struct {
	ID     string
	Label  string
	KeyEnv string
	IsHost bool
	// spec is the registry row, kept so the flow can build a client through
	// the same constructor every other call site uses. Without it,
	// fetchModelsCmd needed its own switch over provider names, and an
	// unrecognised name left p nil and panicked on the first method call.
	spec *providers.Spec
}

func connectProviders() []connectProv {
	// Derived from the registry: locality, key env var and construction all
	// come from the same row that the picker, the cost table and the fallback
	// chain use. A provider added there appears here.
	out := make([]connectProv, 0, len(providers.All()))
	for i := range providers.All() {
		s := providers.Get(providers.All()[i].ID)
		out = append(out, connectProv{
			ID:     s.ID,
			Label:  s.Short,
			KeyEnv: s.KeyEnv,
			IsHost: s.Local,
			spec:   s,
		})
	}
	return out
}

type connectResult struct {
	Provider string
	Model    string
	Key      string
	KeyEnv   string
	Host     string
	SaveKey  bool
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

// maskKey hides all but the first two and last four characters of a
// credential. Rune-aware, because a partial character at either end would leak
// a fragment of the secret into the transcript.
func maskKey(s string) string {
	const dots = "•••"
	r := []rune(s)
	if len(r) <= 8 {
		return dots
	}
	return string(r[:2]) + dots + string(r[len(r)-4:])
}

// fetchModelsCmd verifies the credential and lists models (registry fallback).
func (m *Model) fetchModelsCmd(prov connectProv, cred string) tea.Cmd {
	host := m.cfg.OllamaHost
	if prov.IsHost && cred != "" {
		host = cred
	}
	spec := prov.spec
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		// Built through the registry, so there is no switch over provider
		// names to fall out of step. An unknown id yields nil here and is
		// reported below rather than dereferenced.
		var p providers.Provider
		if spec != nil {
			p = spec.New(cred, host)
		}
		if p != nil {
			if ms, err := p.ListModels(ctx); err == nil && len(ms) > 0 {
				return fetchModelsMsg{models: ms}
			} else if err != nil && prov.ID == "ollama" {
				return fetchModelsMsg{note: "unreachable: " + err.Error()}
			}
		} else {
			return fetchModelsMsg{note: "unknown provider " + prov.ID}
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
	if c.step == cStepKey || c.step == cStepSave {
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
		if p.IsHost {
			c.done = true
			return nil
		}
		c.keyInput.SetValue("")
		c.keyInput.Placeholder = "Save key to OS keyring? (Y/n)"
		c.keyInput.EchoMode = textinput.EchoNormal
		c.step = cStepSave
		c.keyInput.Focus()
		return textinput.Blink
	case cStepSave:
		ans := strings.ToLower(strings.TrimSpace(c.keyInput.Value()))
		c.result.SaveKey = ans == "" || ans == "y" || ans == "yes"
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
		// Only the provider that was connected. A switch that filled every
		// provider's key would send this secret to hosts the user never chose.
		m.cfg.SetKeyFor(r.Provider, r.Key)
	}
	out := applyModel(m, r.Provider, r.Model)
	if r.Provider == "ollama" {
		return out + " (host " + m.cfg.OllamaHost + ")"
	}
	extra := "key " + maskKey(r.Key) + " active for this session"
	if r.SaveKey {
		if err := keys.Set(r.KeyEnv, r.Key); err != nil {
			extra += " - keyring save failed (" + err.Error() + "), persist with: export " + r.KeyEnv + "=..."
		} else {
			extra += " - saved to OS keyring"
		}
	} else {
		extra += " - persist with: export " + r.KeyEnv + "=..."
	}
	return out + " (" + extra + ")"
}

func (c *connectFlow) view(width int, accent lipgloss.Color) string {
	if width < 30 {
		width = 30
	}
	if width > 64 {
		width = 64
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Connect provider")
	steps := []string{"provider", "key", "models", "save"}
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
	case cStepSave:
		fmt.Fprintf(&b, "%s\n%s\n",
			dim.Render(c.result.Provider+" / "+c.result.Model+" · key "+maskKey(c.result.Key)),
			"Save this key to the OS keyring?")
		b.WriteString(c.keyInput.View() + "\n")
		if c.err != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Render(c.err) + "\n")
		}
	case cStepModels:
		if c.note != "" {
			b.WriteString(dim.Render(c.note) + "\n")
		}
		start, end := window(c.modelIdx, len(c.models), 8)
		for i := start; i < end; i++ {
			name := c.models[i].ID
			if len(name) > width-10 {
				name = textutil.Truncate(name, width-10)
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
