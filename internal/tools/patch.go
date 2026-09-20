package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type PatchTool struct{ Workdir string }

func (PatchTool) Name() string { return "patch" }
func (PatchTool) Description() string {
	return "Apply a unified diff to the repo (git apply). Args: diff (required). Refused outside a git repo or in read-only mode."
}
func (PatchTool) Schema() string {
	return `{"type":"object","required":["diff"],"properties":{"diff":{"type":"string"}}}`
}

func (t *PatchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Diff string `json:"diff"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("patch is blocked in read-only mode")
	}
	if a.Diff == "" {
		return "", fmt.Errorf("diff required")
	}
	if _, err := os.Stat(filepath.Join(t.Workdir, ".git")); err != nil {
		return "", fmt.Errorf("not a git repo: %s", t.Workdir)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=fix", "-")
	cmd.Dir = t.Workdir
	cmd.Stdin = strings.NewReader(a.Diff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("patch failed (check paths/hunks): %v", err)
	}
	if len(out) == 0 {
		return "patched ok", nil
	}
	return string(out), nil
}
