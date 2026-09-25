package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
)

const fileTreeWidth = 22

func (m *Model) fileTreeView(height int) string {
	raw := yctx.Tree(m.workdir, 80, 2000)
	if strings.TrimSpace(raw) == "" {
		raw = "(empty)"
	}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	// truncate long names to fit
	for i, l := range lines {
		if len(l) > fileTreeWidth-4 {
			lines[i] = l[:fileTreeWidth-7] + "…"
		}
	}
	content := strings.Join(lines, "\n")
	vp := viewport.New(fileTreeWidth, height)
	vp.SetContent(content)
	title := lipgloss.NewStyle().Bold(true).Foreground(m.th.Accent).Render(" Files")
	body := vp.View()
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, false, false, true).
		BorderForeground(m.th.Accent).
		Width(fileTreeWidth).
		Height(height).
		Padding(0, 1).
		Render(title + "\n" + body)
}
