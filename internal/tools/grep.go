package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxGrepHits = 200

type GrepTool struct{}

func (GrepTool) Name() string { return "grep" }
func (GrepTool) Description() string {
	return "Regex search over files. Args: pattern (required), path (dir or file, default .), include (glob like *.go, optional)."
}
func (GrepTool) Schema() string {
	return `{"type":"object","required":["pattern"],"properties":{"pattern":{"type":"string"},"path":{"type":"string"},"include":{"type":"string"}}}`
}

func (GrepTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", err
	}
	root := a.Path
	if root == "" {
		root = "."
	}
	var out strings.Builder
	hits := 0
	walk := func(path string, d os.DirEntry, err error) error {
		if err != nil || hits >= maxGrepHits {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == "node_modules" || n == "__pycache__" || n == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if a.Include != "" {
			ok, _ := filepath.Match(a.Include, filepath.Base(path))
			if !ok {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 2*1024*1024 {
			return nil
		}
		for i, line := range splitLines(string(data)) {
			if re.MatchString(line) {
				fmt.Fprintf(&out, "%s:%d: %s\n", path, i+1, truncate(line, 300))
				hits++
				if hits >= maxGrepHits {
					break
				}
			}
		}
		return nil
	}
	fi, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		_ = walk(root, fileEntry(root), nil)
	} else {
		_ = filepath.WalkDir(root, walk)
	}
	if hits == 0 {
		return "(no matches)", nil
	}
	return out.String(), nil
}
