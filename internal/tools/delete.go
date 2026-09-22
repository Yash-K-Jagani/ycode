package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DeleteTool struct{ Workdir string }

func (DeleteTool) Name() string { return "delete" }
func (DeleteTool) Description() string {
	return "Delete a file or directory. Args: path (required), recursive (bool, required for dirs), force (bool, allow outside workdir). Refuses workdir root and .git. Blocked read-only."
}
func (DeleteTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"recursive":{"type":"boolean"},"force":{"type":"boolean"}}}`
}

func (t *DeleteTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Force     bool   `json:"force"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("delete is blocked in read-only mode")
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", fmt.Errorf("path required")
	}
	p := resolve(t.Workdir, a.Path)
	clean := filepath.Clean(p)
	if t.Workdir != "" {
		wd, err := filepath.Abs(t.Workdir)
		if err == nil {
			rel, err := filepath.Rel(wd, clean)
			if err != nil || rel == "." {
				return "", fmt.Errorf("refusing to delete the workdir itself")
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if !a.Force {
					return "", fmt.Errorf("refusing path outside workdir (use force:true to override): %s", a.Path)
				}
			}
		}
	}
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".git" {
			return "", fmt.Errorf("refusing to delete .git content")
		}
	}
	fi, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("not found: %s", a.Path)
	}
	if fi.IsDir() && !a.Recursive {
		return "", fmt.Errorf("is a directory (use recursive:true): %s", a.Path)
	}
	if err := os.RemoveAll(clean); err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted %s", a.Path), nil
}
