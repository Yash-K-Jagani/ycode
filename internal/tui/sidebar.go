package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

const sideWidth = 30

func shortTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// ctxBar renders a 10-cell usage bar like ▓▓▓▓▓▓░░░░ 62% (3.7k/6k).
func ctxBar(used, budget int) string {
	if budget <= 0 {
		return "—"
	}
	pct := used * 100 / budget
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * 10 / 100
	return strings.Repeat("▓", filled) + strings.Repeat("░", 10-filled) +
		fmt.Sprintf(" %d%% (%s/%s)", pct, shortTokens(used), shortTokens(budget))
}

func (m *Model) sidebar(height int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(m.th.Accent)
	dim := lipgloss.NewStyle().Foreground(m.th.Dim)
	val := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#222222", Dark: "#DDDDDD"})
	var b strings.Builder
	sec := func(title string) {
		b.WriteString(head.Render("▸ "+title) + "\n")
	}
	folder := filepath.Base(m.workdir)
	if folder == "" || folder == "." {
		folder = m.workdir
	}
	sec("folder")
	b.WriteString(val.Render(truncSide(folder)) + "\n")
	sec("model")
	b.WriteString(val.Render(truncSide(m.cfg.ActiveProvider+"/"+m.cfg.ActiveModel)) + "\n")
	sec("tokens")
	fmt.Fprintf(&b, "%s\n", val.Render(shortTokens(m.sessPTok)+" in · "+shortTokens(m.sessCTok)+" out"))
	if !cost.Free(m.cfg.ActiveProvider) {
		sec("context")
		b.WriteString(val.Render(ctxBar(m.lastCtx, m.lastCtxB)) + "\n")
	}
	sec("spent")
	_, _, today := m.tracker.Today()
	spend := fmt.Sprintf("$%.4f sess · $%.4f today", m.sessUSD, today)
	if cost.Free(m.cfg.ActiveProvider) {
		spend = fmt.Sprintf("free local · $%.4f today (cloud)", today)
	}
	fmt.Fprintf(&b, "%s\n", val.Render(spend))
	sec("tasks")
	todos := tools.ReadTodos(m.workdir)
	if len(todos) == 0 {
		b.WriteString(dim.Render("no tasks — big job? I break it down in build") + "\n")
	} else {
		n := 0
		for _, t := range todos {
			if n >= 8 {
				fmt.Fprintf(&b, "%s\n", dim.Render(fmt.Sprintf("…+%d more", len(todos)-n)))
				break
			}
			mark := "[ ]"
			st := val
			if t.Done {
				mark = "[x]"
				st = dim
			}
			fmt.Fprintf(&b, "%s\n", st.Render(mark+" "+truncSide(fmt.Sprintf("%d. %s", t.ID, t.Text))))
			n++
		}
	}
	sec("queue")
	q := batch.Load().List()
	pending := 0
	for _, j := range q {
		if j.Status == "queued" {
			pending++
		}
	}
	if pending == 0 {
		b.WriteString(dim.Render("no queued jobs") + "\n")
	} else {
		b.WriteString(val.Render(fmt.Sprintf("%d queued", pending)) + "\n")
	}
	body := b.String()
	lines := strings.Count(body, "\n")
	for lines < height-2 && height > 0 {
		body += "\n"
		lines++
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, false, false, true).
		BorderForeground(m.th.Accent).
		Width(sideWidth).
		Height(height).
		Padding(0, 1).
		Render(strings.TrimRight(body, "\n"))
}

func truncSide(s string) string {
	if len(s) > sideWidth-6 {
		return s[:sideWidth-9] + "…"
	}
	return s
}
