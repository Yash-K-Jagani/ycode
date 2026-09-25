package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// RemoveTool is the dedicated removal tool (same safety rails as delete).
type RemoveTool struct{ Workdir string }

func (RemoveTool) Name() string { return "remove" }
func (RemoveTool) Description() string {
	return "Delete a file or directory. Args: path (required), recursive (bool, required for dirs), force (bool, allow outside workdir). Refuses workdir root and .git. Blocked read-only."
}
func (RemoveTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"recursive":{"type":"boolean"},"force":{"type":"boolean"}}}`
}

func (t *RemoveTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
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
