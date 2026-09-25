package ctx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Brief returns a compact codebase brief for prompts: README head, agent
// instructions, and git branch/status. Capped; "" when nothing is found.
func Brief(workdir string) string {
	var b strings.Builder
	if head := readHead(workdir, []string{"README.md", "readme.md", "Readme.md"}, 40, 800); head != "" {
		b.WriteString("\nREADME:\n" + head + "\n")
	}
	if head := readHead(workdir, []string{"AGENTS.md", "CLAUDE.md"}, 30, 600); head != "" {
		b.WriteString("\nAGENT INSTRUCTIONS:\n" + head + "\n")
	}
	if gs := gitState(workdir); gs != "" {
		b.WriteString("\nGIT:\n" + gs + "\n")
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 2000 {
		out = out[:2000] + "\n…(trimmed)"
	}
	return out
}

func readHead(workdir string, names []string, maxLines, maxChars int) string {
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(workdir, n))
		if err != nil || len(data) == 0 {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		s := strings.TrimSpace(strings.Join(lines, "\n"))
		if len(s) > maxChars {
			s = s[:maxChars]
		}
		if s != "" {
			return s
		}
	}
	return ""
}

func gitState(workdir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	branch, err := exec.CommandContext(ctx, "git", "-C", workdir, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "branch %s\n", strings.TrimSpace(string(branch)))
	st, err := exec.CommandContext(ctx, "git", "-C", workdir, "status", "--short").Output()
	if err != nil {
		return strings.TrimSpace(b.String())
	}
	lines := strings.Split(strings.TrimSpace(string(st)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		b.WriteString("clean\n")
		return strings.TrimSpace(b.String())
	}
	if len(lines) > 10 {
		lines = append(lines[:10], fmt.Sprintf("…(%d more)", len(lines)-10))
	}
	b.WriteString(strings.Join(lines, "\n"))
	return strings.TrimSpace(b.String())
}
