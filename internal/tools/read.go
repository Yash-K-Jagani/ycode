package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

const maxReadBytes = 100 * 1024

type ReadTool struct{}

func (ReadTool) Name() string { return "read" }
func (ReadTool) Description() string {
	return "Read a file (or directory listing). Args: path (required), offset (1-based line, default 1), limit (max lines, default 2000)."
}
func (ReadTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}}}`
}

func (ReadTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	fi, err := os.Stat(a.Path)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		entries, err := os.ReadDir(a.Path)
		if err != nil {
			return "", err
		}
		out := ""
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			out += name + "\n"
		}
		return out, nil
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}
	lines := splitLines(string(data))
	if a.Offset <= 0 {
		a.Offset = 1
	}
	if a.Limit <= 0 {
		a.Limit = 2000
	}
	start := a.Offset - 1
	if start >= len(lines) {
		return "(empty range)", nil
	}
	end := start + a.Limit
	if end > len(lines) {
		end = len(lines)
	}
	out := ""
	for i := start; i < end; i++ {
		out += fmt.Sprintf("%d: %s\n", i+1, lines[i])
	}
	if len(lines) > end {
		out += fmt.Sprintf("… (%d more lines)", len(lines)-end)
	}
	return out, nil
}
