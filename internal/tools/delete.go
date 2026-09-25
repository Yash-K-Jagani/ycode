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
	out, err := removePath(ctx, t.Workdir, a.Path, a.Recursive, a.Force)
	if err != nil {
		return "", err
	}
	return out + fmt.Sprintf("\n```diff\n- %s\n```", a.Path), nil
}

// removePath implements the shared delete/remove rails.
func removePath(ctx context.Context, workdir, rawPath string, recursive, force bool) (string, error) {
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("delete is blocked in read-only mode")
	}
	if strings.TrimSpace(rawPath) == "" {
		return "", fmt.Errorf("path required")
	}
	p := resolve(workdir, rawPath)
	clean := filepath.Clean(p)
	if workdir != "" {
		wd, err := filepath.Abs(workdir)
		if err == nil {
			rel, err := filepath.Rel(wd, clean)
			if err != nil || rel == "." {
				return "", fmt.Errorf("refusing to delete the workdir itself")
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if !force {
					return "", fmt.Errorf("refusing path outside workdir (use force:true to override): %s", rawPath)
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
		return "", fmt.Errorf("not found: %s", rawPath)
	}
	if fi.IsDir() && !recursive {
		return "", fmt.Errorf("is a directory (use recursive:true): %s", rawPath)
	}
	if err := os.RemoveAll(clean); err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted %s", rawPath), nil
}
