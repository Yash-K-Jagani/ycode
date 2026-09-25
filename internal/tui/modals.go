package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/store"
)

// ---------- Models modal ----------

type modelsModal struct {
	entries  []modelEntry
	filtered []modelEntry
	input    textinput.Model
	sel      int
	note     string
}

func newModelsModal(entries []modelEntry, note string) *modelsModal {
	ti := textinput.New()
	ti.Placeholder = "filter models…"
	ti.CharLimit = 64
	ti.Focus()
	m := &modelsModal{entries: entries, filtered: entries, input: ti, note: note}
	return m
}

func (m *modelsModal) update(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.sel > 0 {
				m.sel--
			}
		case "down":
			if m.sel < len(m.filtered)-1 {
				m.sel++
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	_ = cmd
	// re-filter
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		m.filtered = m.entries
	} else {
		names := make([]string, len(m.entries))
		for i, e := range m.entries {
			names[i] = e.Provider + "/" + e.Model
		}
		matches := fuzzy.Find(q, names)
		var out []modelEntry
		for _, ma := range matches {
			out = append(out, m.entries[ma.Index])
		}
		m.filtered = out
		if m.sel >= len(m.filtered) {
			m.sel = len(m.filtered) - 1
		}
		if m.sel < 0 {
			m.sel = 0
		}
	}
}

func (m *modelsModal) view(width int, accent lipgloss.Color) string {
	if width < 30 {
		width = 30
	}
	if width > 70 {
		width = 70
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Models")
	var b strings.Builder
	b.WriteString(title + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	if m.note != "" {
		b.WriteString(lipgloss.NewStyle().Faint(true).Render(m.note) + "\n")
	}
	const maxShow = 8
	start := 0
	if m.sel >= maxShow {
		start = m.sel - maxShow + 1
	}
	end := start + maxShow
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	if len(m.filtered) == 0 {
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("(no matches)") + "\n")
	}
	for i := start; i < end; i++ {
		e := m.filtered[i]
		tag := ""
		if e.Installed {
			tag = " ●"
		}
		line := fmt.Sprintf("%s/%s%s", e.Provider, e.Model, tag)
		if len(line) > width-6 {
			line = line[:width-9] + "…"
		}
		if i == m.sel {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render("▸ "+line) + "\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"}).Render("  "+line) + "\n")
		}
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("↑↓ move · enter select · esc close"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Width(width).Padding(0, 1)
	return box.Render(b.String())
}

// ---------- Sessions modal ----------

type sessionsModal struct {
	entries  []sessions.Session
	filtered []sessions.Session
	input    textinput.Model
	sel      int
}

func newSessionsModal(entries []sessions.Session) *sessionsModal {
	ti := textinput.New()
	ti.Placeholder = "filter sessions…"
	ti.CharLimit = 64
	ti.Focus()
	return &sessionsModal{entries: entries, filtered: entries, input: ti}
}

func (m *sessionsModal) update(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.sel > 0 {
				m.sel--
			}
		case "down":
			if m.sel < len(m.filtered)-1 {
				m.sel++
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	_ = cmd
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if q == "" {
		m.filtered = m.entries
	} else {
		names := make([]string, len(m.entries))
		for i, s := range m.entries {
			names[i] = s.Title + " " + s.Provider + "/" + s.Model + " " + s.ID
		}
		matches := fuzzy.Find(q, names)
		var out []sessions.Session
		for _, ma := range matches {
			out = append(out, m.entries[ma.Index])
		}
		m.filtered = out
		if m.sel >= len(m.filtered) {
			m.sel = len(m.filtered) - 1
		}
		if m.sel < 0 {
			m.sel = 0
		}
	}
}

func (m *sessionsModal) view(width int, accent lipgloss.Color) string {
	if width < 30 {
		width = 30
	}
	if width > 70 {
		width = 70
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Sessions")
	var b strings.Builder
	b.WriteString(title + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	const maxShow = 8
	start := 0
	if m.sel >= maxShow {
		start = m.sel - maxShow + 1
	}
	end := start + maxShow
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	if len(m.filtered) == 0 {
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("(no matches)") + "\n")
	}
	for i := start; i < end; i++ {
		s := m.filtered[i]
		line := fmt.Sprintf("%s (%s/%s) %d msgs", s.Title, s.Provider, s.Model, len(s.Messages))
		if len(line) > width-6 {
			line = line[:width-9] + "…"
		}
		if i == m.sel {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render("▸ "+line) + "\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"}).Render("  "+line) + "\n")
		}
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("↑↓ move · enter resume · esc close"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Width(width).Padding(0, 1)
	return box.Render(b.String())
}

// ---------- Store modal ----------

type storeModal struct {
	entries  []store.Entry
	filtered []store.Entry
	input    textinput.Model
	sel      int
}

func newStoreModal(entries []store.Entry) *storeModal {
	ti := textinput.New()
	ti.Placeholder = "filter store…"
	ti.CharLimit = 64
	ti.Focus()
	return &storeModal{entries: entries, filtered: entries, input: ti}
}

func (m *storeModal) update(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.sel > 0 {
				m.sel--
			}
		case "down":
			if m.sel < len(m.filtered)-1 {
				m.sel++
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	_ = cmd
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		m.filtered = m.entries
	} else {
		names := make([]string, len(m.entries))
		for i, e := range m.entries {
			names[i] = e.Name + " " + e.Kind + " " + e.Description
		}
		matches := fuzzy.Find(q, names)
		var out []store.Entry
		for _, ma := range matches {
			out = append(out, m.entries[ma.Index])
		}
		m.filtered = out
		if m.sel >= len(m.filtered) {
			m.sel = len(m.filtered) - 1
		}
		if m.sel < 0 {
			m.sel = 0
		}
	}
}

func (m *storeModal) view(width int, accent lipgloss.Color) string {
	if width < 30 {
		width = 30
	}
	if width > 70 {
		width = 70
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Store")
	var b strings.Builder
	b.WriteString(title + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	const maxShow = 8
	start := 0
	if m.sel >= maxShow {
		start = m.sel - maxShow + 1
	}
	end := start + maxShow
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	if len(m.filtered) == 0 {
		b.WriteString(lipgloss.NewStyle().Faint(true).Render("(no matches)") + "\n")
	}
	for i := start; i < end; i++ {
		e := m.filtered[i]
		line := fmt.Sprintf("%s [%s] %s", e.Name, e.Kind, e.Description)
		if len(line) > width-6 {
			line = line[:width-9] + "…"
		}
		if i == m.sel {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render("▸ "+line) + "\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"}).Render("  "+line) + "\n")
		}
	}
	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("↑↓ move · enter install · esc close"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Width(width).Padding(0, 1)
	return box.Render(b.String())
}
