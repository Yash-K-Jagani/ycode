package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type TreeTool struct{}

func (TreeTool) Name() string { return "tree" }
func (TreeTool) Description() string {
	return "Directory tree listing. Args: path (default .), depth (default 3). Read-only."
}
func (TreeTool) Schema() string {
	return `{"type":"object","properties":{"path":{"type":"string"},"depth":{"type":"integer"}}}`
}

func (TreeTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	root := a.Path
	if root == "" {
		root = "."
	}
	depth := a.Depth
	if depth <= 0 {
		depth = 3
	}
	var b strings.Builder
	count := 0
	var walk func(dir string, level int, prefix string) error
	walk = func(dir string, level int, prefix string) error {
		if level > depth || count > 300 {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() && skipDir(e.Name()) {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for i, n := range names {
			last := i == len(names)-1
			branch := "├─ "
			next := prefix + "│  "
			if last {
				branch = "└─ "
				next = prefix + "   "
			}
			full := filepath.Join(dir, n)
			fi, err := os.Stat(full)
			if err != nil {
				continue
			}
			disp := n
			if fi.IsDir() {
				disp += "/"
			}
			b.WriteString(prefix + branch + disp + "\n")
			count++
			if count > 300 {
				b.WriteString(prefix + "…(truncated)\n")
				return nil
			}
			if fi.IsDir() {
				_ = walk(full, level+1, next)
			}
		}
		return nil
	}
	base := strings.TrimSuffix(root, "/")
	b.WriteString(base + "/\n")
	_ = walk(root, 1, "")
	return b.String(), nil
}

func skipDir(n string) bool {
	switch n {
	case ".git", "node_modules", "__pycache__", ".venv", "dist", "build", "target", ".idea", ".vscode":
		return true
	}
	return false
}
