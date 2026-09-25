package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CreateTool struct{ Workdir string }

func (CreateTool) Name() string { return "create" }
func (CreateTool) Description() string {
	return "Create a NEW file; fails if it exists (unlike write). Args: path (required), content (required). Creates parent dirs."
}
func (CreateTool) Schema() string {
	return `{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`
}

func (t *CreateTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("create is blocked in read-only mode")
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	p := resolve(t.Workdir, a.Path)
	if _, err := os.Stat(p); err == nil {
		return "", fmt.Errorf("exists (use edit to change it, write to overwrite): %s", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	out := fmt.Sprintf("created %s (%d bytes)\n```diff\n%s```",
		p, len(a.Content), diffBlock(nil, splitLines(a.Content), 60))
	return strings.TrimRight(out, "\n"), nil
}
