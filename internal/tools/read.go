package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const maxReadBytes = 100 * 1024

type ReadTool struct{}

func (ReadTool) Name() string { return "read" }
func (ReadTool) Description() string {
	return "Read files (or a directory listing). Args: path (single file/dir) and/or paths (array of files, max 10). offset/limit apply per file."
}
func (ReadTool) Schema() string {
	return `{"type":"object","properties":{"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"offset":{"type":"integer"},"limit":{"type":"integer"}}}`
}

func (ReadTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string   `json:"path"`
		Paths  []string `json:"paths"`
		Offset int      `json:"offset"`
		Limit  int      `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	paths := a.Paths
	if a.Path != "" {
		paths = append([]string{a.Path}, paths...)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("path or paths required")
	}
	if len(paths) > 10 {
		return "", fmt.Errorf("max 10 paths per call")
	}
	if len(paths) == 1 {
		return readOne(paths[0], a.Offset, a.Limit)
	}
	var b strings.Builder
	for _, p := range paths {
		out, err := readOne(p, a.Offset, a.Limit)
		if err != nil {
			fmt.Fprintf(&b, "=== %s ===\nERROR: %v\n", p, err)
			continue
		}
		fmt.Fprintf(&b, "=== %s ===\n%s", p, out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func readOne(path string, offset, limit int) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		entries, err := os.ReadDir(path)
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}
	lines := splitLines(string(data))
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 {
		limit = 2000
	}
	start := offset - 1
	if start >= len(lines) {
		return "(empty range)", nil
	}
	end := start + limit
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
