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

type GlobTool struct{}

func (GlobTool) Name() string { return "glob" }
func (GlobTool) Description() string {
	return "Find files by glob pattern. Args: pattern (required, e.g. **/*.go), path (base dir, default .)."
}
func (GlobTool) Schema() string {
	return `{"type":"object","required":["pattern"],"properties":{"pattern":{"type":"string"},"path":{"type":"string"}}}`
}

func (GlobTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if a.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	base := a.Path
	if base == "" {
		base = "."
	}
	matches, err := globWalk(base, a.Pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "(no files)", nil
	}
	if len(matches) > maxGrepHits {
		matches = matches[:maxGrepHits]
	}
	return strings.Join(matches, "\n"), nil
}

func globWalk(base, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	hasDoublestar := strings.Contains(pattern, "**")
	var out []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel, hasDoublestar) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func matchGlob(pattern, s string, _ bool) bool {
	if ok, _ := filepath.Match(pattern, s); ok {
		return true
	}
	pSeg := strings.Split(filepath.ToSlash(pattern), "/")
	sSeg := strings.Split(filepath.ToSlash(s), "/")
	if matchSegs(pSeg, sSeg) {
		return true
	}
	// fallback: match basename only (e.g. "*.go")
	base := sSeg[len(sSeg)-1]
	ok, _ := filepath.Match(pSeg[len(pSeg)-1], base)
	return ok
}

func matchSegs(p, s []string) bool {
	if len(p) == 0 {
		return len(s) == 0
	}
	if p[0] == "**" {
		for i := 0; i <= len(s); i++ {
			if matchSegs(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 {
		return false
	}
	ok, _ := filepath.Match(p[0], s[0])
	return ok && matchSegs(p[1:], s[1:])
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type fileEntry string

func (f fileEntry) Name() string               { return filepath.Base(string(f)) }
func (f fileEntry) IsDir() bool                { return false }
func (f fileEntry) Type() os.FileMode          { return 0 }
func (f fileEntry) Info() (os.FileInfo, error) { return os.Stat(string(f)) }
