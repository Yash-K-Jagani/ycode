package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type AddTool struct{ Workdir string }

func (AddTool) Name() string { return "add" }
func (AddTool) Description() string {
	return "Append text to the end of a file (creates it with parent dirs if absent). Args: path (required), content (required)."
}
func (AddTool) Schema() string {
	return `{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`
}

func (t *AddTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("add is blocked in read-only mode")
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	p := resolve(t.Workdir, a.Path)
	var old []string
	if data, err := os.ReadFile(p); err == nil {
		old = splitLines(string(data))
		backupFile(t.Workdir, p, data)
	} else {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	text := a.Content
	if len(old) > 0 && !strings.HasSuffix(strings.Join(old, "\n"), "\n") {
		text = "\n" + text
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return "", err
	}
	_ = f.Close()
	_ = tryFormat(p)
	added := splitLines(a.Content)
	out := fmt.Sprintf("appended %d lines to %s\n```diff\n%s```", len(added), p, diffBlock(old, append(old, added...), 60))
	return strings.TrimRight(out, "\n"), nil
}
