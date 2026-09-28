package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/charmbracelet/lipgloss"
)

// renderAll rebuilds the transcript pane from the session. Called whenever the
// session is replaced wholesale - a resume, a reload - rather than appended to
// incrementally.
func (m *Model) renderAll() {
	m.msgs = nil
	for _, msg := range m.sess.Messages {
		m.msgs = append(m.msgs, m.formatMsg(msg.Role, msg.Content))
	}
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

func (m Model) View() string {
	badge := ""
	if m.cfg.ZeroDataLeak {
		badge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555")).Render(" [LOCAL ONLY]")
	}
	chip := lipgloss.NewStyle().Bold(true).Foreground(m.th.Accent).Render("▸ "+string(m.mode)) +
		lipgloss.NewStyle().Foreground(m.th.Dim).Render(" · "+m.agent.Name+" · tab: switch mode")
	if g := m.goal; g != nil {
		st := lipgloss.NewStyle().Foreground(m.th.Dim)
		if g.Status == goal.Active && g.Running() {
			st = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2de1a7"))
		}
		chip += st.Render(fmt.Sprintf(" · goal %d/%d %s", g.Iter, g.MaxIter, g.Status))
	}
	if m.watching() {
		chip += lipgloss.NewStyle().Foreground(lipgloss.Color("#2de1a7")).Render(" · ● watching " + m.watchTarget)
	}
	input := chip + "\n" + m.ta.View()
	chat := m.vp.View()
	if m.showTree {
		chat = lipgloss.JoinHorizontal(lipgloss.Top, m.fileTreeView(m.vp.Height), chat)
	}
	if m.sideOn && m.winW > sideWidth+40 {
		chat = lipgloss.JoinHorizontal(lipgloss.Top, chat, m.sidebar(m.vp.Height))
	}
	out := chat + "\n"
	if pal := m.paletteItems(); len(pal) > 0 {
		out += renderPalette(pal, m.palIdx, m.vp.Width, m.th.Accent) + "\n"
	} else if at, _ := m.atItems(); len(at) > 0 {
		out += renderPalette(at, m.atIdx, m.vp.Width, m.th.Accent) + "\n"
	}
	statusText := fmt.Sprintf(" %s/%s · %d msgs · /help ", m.cfg.ActiveProvider, m.cfg.ActiveModel, len(m.sess.Messages))
	if m.busy {
		statusText += fmt.Sprintf("· %s %.1fs", m.sp.View(), time.Since(m.busySince).Seconds())
	}
	status := lipgloss.NewStyle().Foreground(m.th.Dim).Render(statusText) + badge
	base := out + input + "\n" + status
	if t := m.renderToasts(); t != "" {
		base = t + "\n" + base
	}
	if m.modelsModal != nil {
		w, h := m.winW, m.winH
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.modelsModal.view(w-8, m.th.Accent))
	}
	if m.sessionsModal != nil {
		w, h := m.winW, m.winH
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.sessionsModal.view(w-8, m.th.Accent))
	}
	if m.storeModal != nil {
		w, h := m.winW, m.winH
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.storeModal.view(w-8, m.th.Accent))
	}
	if m.conn != nil {
		w, h := m.winW, m.winH
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.conn.view(w-8, m.th.Accent))
	}
	return base
}
