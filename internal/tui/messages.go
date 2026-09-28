package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) formatMsg(role apitypes.Role, content string) string {
	if role == apitypes.RoleUser {
		st := lipgloss.NewStyle().Foreground(m.th.Accent).Bold(true)
		return st.Render("you › ") + content
	}
	if role == apitypes.RoleSystem {
		st := lipgloss.NewStyle().Foreground(m.th.Dim).Italic(true)
		return st.Render(content)
	}
	return renderAssistant(content)
}

func (m *Model) appendSys(s string) {
	st := lipgloss.NewStyle().Foreground(m.th.Dim).Background(sysBG).Padding(0, 1)
	m.msgs = append(m.msgs, st.Render(s))
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

// appendFileCard renders a write/edit as a distinct card: filename on top.
func (m *Model) appendFileCard(f fileOpMsg) {
	mark := "✓"
	if !f.ok {
		mark = "✗"
	}
	path := f.path
	if path == "" {
		path = "(unknown file)"
	}
	head := fileCardHead(f.ok).Render(fmt.Sprintf("%s %s %s", mark, f.op, path))
	card := head + "\n" + fileCardStyle().Render(f.detail)
	if f.diff != "" {
		card += "\n" + renderDiff(f.diff)
	}
	m.msgs = append(m.msgs, card)
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

// appendCmdCard renders a command execution on its own tinted background.
func (m *Model) appendCmdCard(c cmdOpMsg) {
	mark := "✓"
	if !c.ok {
		mark = "✗"
	}
	head := cmdCardHead(c.ok).Render(fmt.Sprintf("%s %s", mark, c.tool))
	body := cmdCardStyle().Render(c.cmd + "\n" + c.detail)
	m.msgs = append(m.msgs, head+"\n"+body)
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

func (m *Model) initProject() string {
	dir := filepath.Join(m.workdir, ".ycode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "init failed: " + err.Error()
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		_ = os.WriteFile(cfgPath, []byte("# ycode project overrides\n# active_provider: ollama\n"), 0o644)
	}
	agentsPath := filepath.Join(m.workdir, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		_ = os.WriteFile(agentsPath, []byte("# Agent instructions\n\n- Be concise.\n- Run tests after edits.\n"), 0o644)
	}
	return "Initialized ycode in " + m.workdir + " (.ycode/config.yaml, AGENTS.md)"
}
