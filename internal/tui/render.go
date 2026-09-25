package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	codeBG = lipgloss.AdaptiveColor{Light: "#E9E9F2", Dark: "#26263A"}
	codeFG = lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#E6E6FA"}
	chipBG = lipgloss.AdaptiveColor{Light: "#DFDFF2", Dark: "#2E2E4A"}
	chipFG = lipgloss.AdaptiveColor{Light: "#333355", Dark: "#CFCFEA"}
	sysBG  = lipgloss.AdaptiveColor{Light: "#EFEFEF", Dark: "#1B1B28"}
	fileBG = lipgloss.AdaptiveColor{Light: "#E4DFF2", Dark: "#2C2340"}
	fileFG = lipgloss.AdaptiveColor{Light: "#2A1F4D", Dark: "#D9CBFF"}
	cmdBG  = lipgloss.AdaptiveColor{Light: "#F3EAD6", Dark: "#38300F"}
	cmdFG  = lipgloss.AdaptiveColor{Light: "#5C4A1F", Dark: "#E8C86A"}
	delBG  = lipgloss.AdaptiveColor{Light: "#F9E2E2", Dark: "#3D1A1A"}
	delFG  = lipgloss.AdaptiveColor{Light: "#8A1F1F", Dark: "#FF7B72"}
	addBG  = lipgloss.AdaptiveColor{Light: "#E1F3E1", Dark: "#1A3320"}
	addFG  = lipgloss.AdaptiveColor{Light: "#1F6B2E", Dark: "#56D364"}
	gutFG  = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#666666"}
)

func cmdCardStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(cmdBG).Foreground(cmdFG).Padding(0, 1)
}

func cmdCardHead(ok bool) lipgloss.Style {
	fg := lipgloss.AdaptiveColor{Light: "#8A6D1F", Dark: "#E8C86A"}
	if !ok {
		fg = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#FF5555"}
	}
	return lipgloss.NewStyle().Bold(true).Background(cmdBG).Foreground(fg).Padding(0, 1)
}

func delLineStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(delBG).Foreground(delFG)
}

func addLineStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(addBG).Foreground(addFG)
}

func gutterStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(gutFG)
}

// extractDiff pulls the first ```diff block body out of tool result text.
func extractDiff(s string) string {
	i := strings.Index(s, "```diff")
	if i < 0 {
		return ""
	}
	rest := s[i+len("```diff"):]
	j := strings.Index(rest, "```")
	if j < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:j])
}

// gutterize prefixes physical lines with a dim right-aligned gutter.
func gutterize(s string) string {
	lines := strings.Split(s, "\n")
	w := len(fmt.Sprintf("%d", len(lines)))
	for i, ln := range lines {
		lines[i] = gutterStyle().Render(fmt.Sprintf(fmt.Sprintf("%%%dd", w), i+1)) + " │ " + ln
	}
	return strings.Join(lines, "\n")
}

// renderDiff colors unified-diff lines: red bg for removals, green bg for
// additions, dim for hunk headers. No gutter (headers carry real numbers).
func renderDiff(body string) string {
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			out = append(out, chipStyle().Render(ln))
		case strings.HasPrefix(ln, "@@"):
			out = append(out, chipStyle().Render(ln))
		case strings.HasPrefix(ln, "+"):
			out = append(out, addLineStyle().Render(ln))
		case strings.HasPrefix(ln, "-"):
			out = append(out, delLineStyle().Render(ln))
		default:
			out = append(out, lipgloss.NewStyle().Faint(true).Render(ln))
		}
	}
	return strings.Join(out, "\n")
}

func fileCardStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(fileBG).Foreground(fileFG).Padding(0, 1)
}

func fileCardHead(ok bool) lipgloss.Style {
	fg := lipgloss.AdaptiveColor{Light: "#1F7A3D", Dark: "#2DE1A7"}
	if !ok {
		fg = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#FF5555"}
	}
	return lipgloss.NewStyle().Bold(true).Background(fileBG).Foreground(fg).Padding(0, 1)
}

// writeOpPath extracts the path from write/edit tool args.
func writeOpPath(args string) string {
	var v struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return ""
	}
	return v.Path
}

func codeStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(codeBG).Foreground(codeFG).Padding(0, 1)
}

func codePad() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1)
}

// highlight renders code with Chroma (dracula). ok=false → use the plain panel.
func highlight(lang, code string) (out string, ok bool) {
	name := strings.ToLower(strings.TrimSpace(lang))
	if name == "" || strings.ContainsAny(name, " \t/\\") {
		return "", false
	}
	l := lexers.Match("x." + name)
	if l == nil {
		l = lexers.Get(name)
	}
	if l == nil {
		return "", false
	}
	it, err := l.Tokenise(nil, code)
	if err != nil {
		return "", false
	}
	f := formatters.Get("terminal16m")
	if f == nil {
		return "", false
	}
	var b strings.Builder
	if err := f.Format(&b, styles.Get("dracula"), it); err != nil {
		return "", false
	}
	return strings.TrimRight(b.String(), "\n"), true
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
			body, langName := splitLang(sg.text)
			if langName != "" {
				b.WriteString(chipStyle().Render(langName) + "\n")
			}
			if strings.EqualFold(langName, "diff") {
				b.WriteString(renderDiff(body) + "\n")
			} else if hl, ok := highlight(langName, body); ok {
				b.WriteString(codePad().Render(gutterize(hl)) + "\n")
			} else {
				b.WriteString(codeStyle().Render(gutterize(strings.TrimRight(body, "\n"))) + "\n")
			}
		} else if strings.TrimSpace(sg.text) != "" {
			b.WriteString(renderTextSegment(sg.text) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func splitLang(body string) (string, string) {
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		if first := strings.TrimSpace(body[:i]); first != "" && !strings.ContainsAny(first, " \t\"'{") {
			return body[i+1:], first
		}
	}
	return body, ""
}
