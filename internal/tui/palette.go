package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type slashItem struct {
	Name string
	Hint string
}

func slashList() []slashItem {
	return []slashItem{
		{"/help", "commands & shortcuts"},
		{"/exit", "quit (state saved)"},
		{"/export", "export transcript"},
		{"/new", "fresh session"},
		{"/models", "switch model"},
		{"/model", "alias of /models"},
		{"/variants", "installed variants"},
		{"/sessions", "resume a session"},
		{"/status", "health, cost, latency"},
		{"/connect", "API key setup"},
		{"/doctor", "diagnose setup"},
		{"/agent", "pick an agent"},
		{"/init", "scaffold .ycode"},
		{"/editor", "open $EDITOR"},
		{"/plan", "plan mode"},
		{"/build", "build mode"},
		{"/chat", "chat mode"},
		{"/thinking", "thinking mode"},
		{"/review", "AI review of diff"},
		{"/test", "run project tests"},
		{"/refactor", "safe refactoring"},
		{"/rag", "local RAG search"},
		{"/mcps", "MCP servers"},
		{"/skills", "skills"},
		{"/store", "skill/plugin store"},
		{"/hooks", "configured hooks"},
		{"/prompts", "prompt library"},
		{"/plugins", "script plugins"},
	}
}

// filterSlash returns matching commands for an input starting with "/".
func filterSlash(input string) []slashItem {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	token := input
	if i := strings.IndexByte(input, ' '); i >= 0 {
		token = input[:i]
	}
	var out []slashItem
	for _, it := range slashList() {
		if strings.HasPrefix(it.Name, token) {
			out = append(out, it)
		}
	}
	return out
}

func renderPalette(items []slashItem, selected, width int, accent lipgloss.Color) string {
	if len(items) == 0 {
		return ""
	}
	const maxShow = 8
	if selected < 0 {
		selected = 0
	}
	if selected >= len(items) {
		selected = len(items) - 1
	}
	// keep selected visible
	start := 0
	if selected >= maxShow {
		start = selected - maxShow + 1
	}
	end := start + maxShow
	if end > len(items) {
		end = len(items)
	}
	if width < 20 {
		width = 20
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Width(width-2).Padding(0, 1)
	var b strings.Builder
	for i := start; i < end; i++ {
		it := items[i]
		name := it.Name
		if len(name) > 16 {
			name = name[:16]
		}
		line := name + strings.Repeat(" ", 18-len(name)) + it.Hint
		if len(line) > width-6 {
			line = line[:width-6] + "…"
		}
		if i == selected {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render("▸ " + line))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"}).Render("  " + line))
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	more := ""
	if len(items) > end {
		more = "\n" + lipgloss.NewStyle().Faint(true).Render("  …more")
	}
	return box.Render(b.String() + more)
}
