package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	codeBG = lipgloss.AdaptiveColor{Light: "#E9E9F2", Dark: "#26263A"}
	codeFG = lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#E6E6FA"}
	chipBG = lipgloss.AdaptiveColor{Light: "#DFDFF2", Dark: "#2E2E4A"}
	chipFG = lipgloss.AdaptiveColor{Light: "#333355", Dark: "#CFCFEA"}
	sysBG  = lipgloss.AdaptiveColor{Light: "#EFEFEF", Dark: "#1B1B28"}
)

func codeStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(codeBG).Foreground(codeFG).Padding(0, 1)
}

func chipStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(chipBG).Foreground(chipFG).Padding(0, 1)
}

type seg struct {
	code bool
	text string
}

// splitFences splits content into alternating prose/code segments.
func splitFences(s string) []seg {
	var out []seg
	for {
		i := strings.Index(s, "```")
		if i < 0 {
			out = append(out, seg{code: false, text: s})
			break
		}
		if i > 0 {
			out = append(out, seg{code: false, text: s[:i]})
		}
		rest := s[i+3:]
		j := strings.Index(rest, "```")
		if j < 0 {
			out = append(out, seg{code: true, text: rest})
			break
		}
		out = append(out, seg{code: true, text: rest[:j]})
		s = rest[j+3:]
	}
	return out
}

func isToolLine(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "<tool") || strings.HasPrefix(t, "tool:") || strings.HasPrefix(t, "```tool")
}

// renderTextSegment renders prose with glamour, tool-call lines as chips.
func renderTextSegment(t string) string {
	var b strings.Builder
	var run []string
	flush := func() {
		if len(run) == 0 {
			return
		}
		joined := strings.Join(run, "\n")
		// glamour parses <...> as HTML and drops unknown tags (e.g. inline
		// <tool:> echoes), so escape brackets first; entities decode back.
		joined = strings.ReplaceAll(strings.ReplaceAll(joined, "<", "&lt;"), ">", "&gt;")
		r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())
		out, err := r.Render(joined)
		if err != nil {
			out = strings.Join(run, "\n")
		}
		b.WriteString(strings.TrimSpace(out) + "\n")
		run = nil
	}
	for _, ln := range strings.Split(t, "\n") {
		if isToolLine(ln) {
			flush()
			b.WriteString(chipStyle().Render(strings.TrimSpace(ln)) + "\n")
		} else {
			run = append(run, ln)
		}
	}
	flush()
	return strings.TrimSpace(b.String())
}

// renderAssistant renders code fences on a lighter panel and tool lines as chips.
func renderAssistant(content string) string {
	var b strings.Builder
	for _, sg := range splitFences(content) {
		if sg.code {
			body := sg.text
			if i := strings.IndexByte(body, '\n'); i >= 0 {
				if first := strings.TrimSpace(body[:i]); first != "" && !strings.ContainsAny(first, " \t\"'{") {
					b.WriteString(chipStyle().Render(first) + "\n")
					body = body[i+1:]
				}
			}
			b.WriteString(codeStyle().Render(strings.TrimRight(body, "\n")) + "\n")
		} else if strings.TrimSpace(sg.text) != "" {
			b.WriteString(renderTextSegment(sg.text) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}
