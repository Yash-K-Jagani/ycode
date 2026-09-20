package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct{ Workdir string }

func (WriteTool) Name() string { return "write" }
func (WriteTool) Description() string {
	return "Create or overwrite a file. Args: path (required), content (required). Creates parent dirs."
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
	return fmt.Sprintf("wrote %s (%d bytes)", p, len(a.Content)), nil
}

type EditTool struct{ Workdir string }

func (EditTool) Name() string { return "edit" }
func (EditTool) Description() string {
	return "Exact string replacement in a file. Args: path (required), old_string (required, must match exactly once), new_string (required)."
}
func (EditTool) Schema() string {
	return `{"type":"object","required":["path","old_string","new_string"],"properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}}}`
}

func (t *EditTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("edit is blocked in read-only mode")
	}
	p := resolve(t.Workdir, a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	s := string(data)
	n := countOccurrences(s, a.OldString)
	if n == 0 {
		return "", fmt.Errorf("old_string not found in %s", p)
	}
	if n > 1 {
		return "", fmt.Errorf("old_string matches %d times in %s — be more specific", n, p)
	}
	s = replaceOnce(s, a.OldString, a.NewString)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", p), nil
}

func resolve(workdir, p string) string {
	if filepath.IsAbs(p) || workdir == "" {
		return p
	}
	return filepath.Join(workdir, p)
}

func countOccurrences(s, sub string) int {
	if sub == "" {
		return 0
	}
	n := 0
	for i := 0; i+len(sub) <= len(s); {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub)
		} else {
			i++
		}
	}
	return n
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}
