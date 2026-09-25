package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ChangesTool shows the working-tree change overview (git status + diff stat).
type ChangesTool struct{ Workdir string }

func (ChangesTool) Name() string { return "changes" }
func (ChangesTool) Description() string {
	return "Show what changed in the repo (git status + diff stat, read-only). Args: none."
}
func (ChangesTool) Schema() string {
	return `{"type":"object","properties":{}}`
}

func (t *ChangesTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	run := func(argv ...string) string {
		cmd := exec.CommandContext(ctx, "git", argv...)
		if t.Workdir != "" {
			cmd.Dir = t.Workdir
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	var b strings.Builder
	b.WriteString("branch: " + run("branch", "--show-current") + "\n")
	status := run("status", "--short")
	if status == "" {
		return b.String() + "clean\n", nil
	}
	lines := strings.Split(status, "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], fmt.Sprintf("…(%d more)", len(lines)-20))
	}
	b.WriteString(strings.Join(lines, "\n") + "\n")
	if stat := run("diff", "--stat", "HEAD", "--"); stat != "" {
		b.WriteString(stat + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}
