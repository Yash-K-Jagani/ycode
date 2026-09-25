package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

type toast struct {
	msg   string
	until time.Time
}

type toastExpireMsg struct{}

func (m *Model) addToast(msg string) {
	m.toasts = append(m.toasts, toast{msg: msg, until: time.Now().Add(3 * time.Second)})
	if m.prog != nil {
		go func() {
			time.Sleep(3100 * time.Millisecond)
			m.prog.Send(toastExpireMsg{})
		}()
	}
}

func (m *Model) renderToasts() string {
	now := time.Now()
	var active []toast
	for _, t := range m.toasts {
		if now.Before(t.until) {
			active = append(active, t)
		}
	}
	m.toasts = active
	if len(active) == 0 {
		return ""
	}
	style := lipgloss.NewStyle().Background(lipgloss.Color("#2C2C3A")).Foreground(lipgloss.Color("#E6E6FA")).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#7C5CFF"))
	var out string
	for _, t := range active {
		out += style.Render(t.msg) + "\n"
	}
	return out
}
