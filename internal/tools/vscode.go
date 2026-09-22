package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type VSCodeTool struct{ Workdir string }

func (VSCodeTool) Name() string { return "vscode" }
func (VSCodeTool) Description() string {
	return "VS Code integration (needs `code` CLI). Args: action (open|extensions), path (for open), line (optional goto)."
}
func (VSCodeTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"path":{"type":"string"},"line":{"type":"integer"}}}`
}

func (t *VSCodeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Line   int    `json:"line"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if _, err := exec.LookPath("code"); err != nil {
		return "", fmt.Errorf("vscode CLI not found (install VS Code + `code` shell command)")
	}
	switch a.Action {
	case "open":
		if a.Path == "" {
			return "", fmt.Errorf("path required")
		}
		target := resolve(t.Workdir, a.Path)
		if a.Line > 0 {
			target += ":" + strconv.Itoa(a.Line)
		}
		cmd := exec.CommandContext(ctx, "code", "--goto", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			return string(out), fmt.Errorf("code open failed: %v", err)
		}
		return "opened " + target + " in VS Code", nil
	case "extensions":
		cmd := exec.CommandContext(ctx, "code", "--list-extensions", "--show-versions")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), err
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 100 {
			lines = lines[:100]
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", fmt.Errorf("unknown vscode action %q", a.Action)
	}
}
