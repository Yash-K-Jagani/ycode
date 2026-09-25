package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WriteTool struct{ Workdir string }

func (WriteTool) Name() string { return "write" }
func (WriteTool) Description() string {
	return "Create or overwrite a file. Args: path (required), content (required). Creates parent dirs. To change part of a file, use edit instead."
}
func (WriteTool) Schema() string {
	return `{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`
}

func (t *WriteTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("write is blocked in read-only mode")
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	p := resolve(t.Workdir, a.Path)
	var old []string
	if data, err := os.ReadFile(p); err == nil {
		old = splitLines(string(data))
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	out := fmt.Sprintf("wrote %s (%d bytes)", p, len(a.Content))
	if len(old) > 0 {
		out += "\n```diff\n" + diffBlock(old, splitLines(a.Content), 60) + "```"
	} else {
		out += "\n```diff\n" + diffBlock(nil, splitLines(a.Content), 60) + "```"
	}
	return strings.TrimRight(out, "\n"), nil
}
