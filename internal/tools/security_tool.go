package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/security"
)

type SecurityTool struct{ Workdir string }

func (SecurityTool) Name() string { return "security" }
func (SecurityTool) Description() string {
	return "Scan files for hardcoded secrets / vulnerabilities. Args: path (file or dir, required). Read-only, safe in plan mode."
}
func (SecurityTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`
}

func (t *SecurityTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	root := resolve(t.Workdir, a.Path)
	if root == "" {
		root = t.Workdir
	}
	var out strings.Builder
	files := 0
	walk := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 1024*1024 {
			return nil
		}
		files++
		for _, f := range security.ScanSecrets(string(data)) {
			fmt.Fprintf(&out, "%s:%d [%s] %s\n", path, f.Line, f.Rule, f.Text)
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
	if out.Len() == 0 {
		return fmt.Sprintf("scanned %d files: no secrets detected", files), nil
	}
	return fmt.Sprintf("scanned %d files:\n%s", files, out.String()), nil
}
