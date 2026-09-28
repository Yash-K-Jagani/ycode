package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

const sideWidth = 30

// sidebarCache holds the values the sidebar reads from disk, so rendering it
// does not.
//
// View runs after every message, and a streaming turn delivers a message per
// token, so the sidebar was reading .ycode/todos.json and querying the batch
// table a few hundred times a second. Measured at 547µs per render, all of it
// I/O for values that do not change while text is arriving.
//
// The cache is dropped whenever a tool result arrives, because that is the only
// thing in the TUI that can change the task list. A short expiry covers the
// remaining case - a batch queued by another process - without reintroducing
// per-token I/O.
const sidebarTTL = 2 * time.Second

type sidebarCache struct {
	at    time.Time
	todos []tools.TodoItem
	queue int
}

func (m *Model) sidebarData() sidebarCache {
	if !m.side.at.IsZero() && time.Since(m.side.at) < sidebarTTL {
		return m.side
	}
	m.side = sidebarCache{
		at:    time.Now(),
		todos: tools.ReadTodos(m.workdir),
		queue: pendingJobs(),
	}
	return m.side
}

// invalidateSidebar forces the next render to re-read. Called where a tool
// result lands, since that is what mutates the task list.
func (m *Model) invalidateSidebar() { m.side = sidebarCache{} }

func pendingJobs() int {
	n := 0
	for _, j := range batch.Load().List() {
		if j.Status == "queued" {
			n++
		}
	}
	return n
}

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
	data := m.sidebarData()
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
	sec("goal")
	if m.goal == nil {
		b.WriteString(dim.Render("no goal — /goal <text>") + "\n")
	} else {
		b.WriteString(val.Render(truncSide(oneLine(m.goal.Text))) + "\n")
		meter := fmt.Sprintf("iter %d/%d", m.goal.Iter, m.goal.MaxIter)
		if m.goal.Status != goal.Active {
			meter += " · " + string(m.goal.Status)
		} else if m.goal.Running() {
			meter += " · running"
		}
		b.WriteString(dim.Render(truncSide(meter)) + "\n")
	}
	sec("tasks")
	todos := data.todos
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
	if data.queue == 0 {
		b.WriteString(dim.Render("no queued jobs") + "\n")
	} else {
		b.WriteString(val.Render(fmt.Sprintf("%d queued", data.queue)) + "\n")
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

// truncSide shortens a value to fit the sidebar panel. Rune-aware: the sidebar
// shows file paths and folder names, which are routinely non-ASCII, and a byte
// slice would cut a character in half and break the panel border.
func truncSide(s string) string { return textutil.Truncate(s, sideWidth-9) }

// oneLine collapses whitespace so a long goal wraps to a single sidebar row.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
