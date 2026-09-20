package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	bashTimeout = 60 * time.Second
	maxOutBytes = 32 * 1024
)

var bashDeny = []string{
	"rm -rf /", "rm -rf ~", "mkfs", "dd if=", ":(){", "shutdown", "reboot",
	"format c:", "format C:", "rd /s /q c:\\windows", "del /f /s /q c:\\windows",
	">/dev/sda", "diskpart",
}

type BashTool struct{ Workdir string }

func NewBashTool(workdir string) *BashTool { return &BashTool{Workdir: workdir} }

func (BashTool) Name() string { return "bash" }
func (BashTool) Description() string {
	return "Run a shell command (60s timeout, 32KB output cap). Args: command (required). Destructive commands are blocked."
}
func (BashTool) Schema() string {
	return `{"type":"object","required":["command"],"properties":{"command":{"type":"string"}}}`
}

func (t *BashTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("bash is blocked in read-only mode")
	}
	if a.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	lower := strings.ToLower(a.Command)
	for _, d := range bashDeny {
		if strings.Contains(lower, strings.ToLower(d)) {
			return "", fmt.Errorf("blocked by safety denylist: %q", d)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}
	if t.Workdir != "" {
		cmd.Dir = t.Workdir
	}
	out, err := cmd.CombinedOutput()
	if len(out) > maxOutBytes {
		out = append(out[:maxOutBytes], []byte("\n…(truncated)")...)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timeout after %s", bashTimeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("exit error: %v\n%s", err, string(out))
	}
	if len(out) == 0 {
		return "(no output)", nil
	}
	return string(out), nil
}
