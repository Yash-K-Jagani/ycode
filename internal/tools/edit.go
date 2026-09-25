package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type EditTool struct{ Workdir string }

func (EditTool) Name() string { return "edit" }
func (EditTool) Description() string {
	return "Surgical string replacement in a file (preferred over write for changes). Args: path (required), old_string (required, must match exactly once — anchor a small unique block, never the whole file), new_string (required)."
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
	if len(data) > 0 && float64(len(a.OldString))/float64(len(data)) > 0.8 {
		return "", fmt.Errorf("old_string covers >80%% of %s — anchor a smaller unique block instead of rewriting the file", p)
	}
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
	out := fmt.Sprintf("edited %s\n```diff\n%s```",
		p, diffBlock(splitLines(a.OldString), splitLines(a.NewString), 60))
	return strings.TrimRight(out, "\n"), nil
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
