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
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
)

// updateOverlay routes input to whichever modal or form is open, and reports
// whether one was. All of them are centered fuzzy pickers with the same
// shape: filter as you type, Esc dismisses, Enter commits the selection, and
// every other key goes to the picker's own update.
//
// This used to be three copies of that switch inlined at the top of Update,
// plus a fourth block for the connect form. They drifted: the sessions picker
// was the only one that reset the active goal, and the store picker was the
// only one that could return a command. Keeping the commit behaviour in one
// function per modal makes that difference explicit instead of positional.
func (m *Model) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch {
	case m.modelsModal != nil:
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				m.modelsModal = nil
			case "enter":
				if len(m.modelsModal.filtered) > 0 {
					e := m.modelsModal.filtered[m.modelsModal.sel]
					m.appendSys(applyModel(m, e.Provider, e.Model))
				}
				m.modelsModal = nil
			}
			return m, nil, true
		}
		m.modelsModal.update(msg)
		return m, nil, true

	case m.sessionsModal != nil:
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				m.sessionsModal = nil
			case "enter":
				if len(m.sessionsModal.filtered) > 0 {
					s := m.sessionsModal.filtered[m.sessionsModal.sel]
					m.resumeSession(s)
				}
				m.sessionsModal = nil
			}
			return m, nil, true
		}
		m.sessionsModal.update(msg)
		return m, nil, true

	case m.storeModal != nil:
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				m.storeModal = nil
			case "enter":
				if len(m.storeModal.filtered) > 0 {
					e := m.storeModal.filtered[m.storeModal.sel]
					m.appendSys(fmt.Sprintf("Installing %s (%s) …", e.Name, e.Kind))
					cmd := m.storeInstallCmd(e)
					m.storeModal = nil
					return m, cmd, true
				}
				m.storeModal = nil
			}
			return m, nil, true
		}
		m.storeModal.update(msg)
		return m, nil, true

	case m.conn != nil:
		if _, ok := msg.(tea.KeyMsg); ok {
			cmd := m.updateConnect(msg)
			if m.conn != nil && m.conn.done {
				res := m.conn.result
				m.conn = nil
				m.appendSys(m.applyConnect(res))
			}
			return m, cmd, true
		}
		if _, ok := msg.(fetchModelsMsg); ok {
			return m, m.updateConnect(msg), true
		}
	}
	return m, nil, false
}

// resumeSession adopts a session from the picker. The previous session's goal
// is dropped: its steps described work in that transcript, and carrying them
// into a new session would let goal mode report progress it never made.
func (m *Model) resumeSession(s sessions.Session) {
	m.sess = &s
	if s.Provider != "" {
		m.cfg.ActiveProvider = s.Provider
	}
	if s.Model != "" {
		m.cfg.ActiveModel = s.Model
	}
	m.router.Update(m.cfg)
	_ = m.cfg.Save()
	m.renderAll()
	m.goal = nil
	m.appendSys("Resumed " + s.Title + " (/goal <text> to start a new goal)")
}

// ---------- Models modal ----------

type modelsModal struct {
	entries  []modelEntry
	filtered []modelEntry
	input    textinput.Model
	sel      int
	note     string
}

// The three pickers differ only in what they list and what committing an entry
// does; moving the selection, filtering and clamping were three copies of the
// same code. These two helpers are that logic, once.

// moveSel moves a selection by delta within a list of n items, staying put at
// the ends rather than wrapping: a picker that wraps makes it easy to commit
// the wrong model by holding down a key.
func moveSel(sel, delta, n int) int {
	if delta < 0 && sel > 0 {
		return sel - 1
	}
	if delta > 0 && sel < n-1 {
		return sel + 1
	}
	return sel
}

// clampSel brings a selection back into range after the filtered list changed
// underneath it. An empty list yields 0, so Enter is a no-op rather than a
// panic on filtered[0].
func clampSel(sel, n int) int {
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		sel = 0
	}
	return sel
}

// pickKey applies an arrow key to a picker's selection, reporting whether the
// key was one it handles.
func pickKey(sel *int, filtered int, msg tea.Msg) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return
	}
	switch k.String() {
	case "up":
		*sel = moveSel(*sel, -1, filtered)
	case "down":
		*sel = moveSel(*sel, 1, filtered)
	}
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
	pickKey(&m.sel, len(m.filtered), msg)
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
	}
	m.sel = clampSel(m.sel, len(m.filtered))
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
			line = textutil.Truncate(line, width-9)
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
	pickKey(&m.sel, len(m.filtered), msg)
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
	}
	m.sel = clampSel(m.sel, len(m.filtered))
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
			line = textutil.Truncate(line, width-9)
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
	pickKey(&m.sel, len(m.filtered), msg)
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
	}
	m.sel = clampSel(m.sel, len(m.filtered))
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
			line = textutil.Truncate(line, width-9)
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
