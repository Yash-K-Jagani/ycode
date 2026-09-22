package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type ScaffoldTool struct{ Workdir string }

func (ScaffoldTool) Name() string { return "scaffold" }
func (ScaffoldTool) Description() string {
	return "Starters for frameworks. Args: template (react|express|fastapi), dest (required). Runs canonical CLIs with long timeout."
}
func (ScaffoldTool) Schema() string {
	return `{"type":"object","required":["template","dest"],"properties":{"template":{"type":"string"},"dest":{"type":"string"}}}`
}

func (t *ScaffoldTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Template string `json:"template"`
		Dest     string `json:"dest"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("scaffold is blocked in read-only mode")
	}
	if a.Dest == "" {
		return "", fmt.Errorf("dest required")
	}
	dest := resolve(t.Workdir, a.Dest)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("destination exists: %s", dest)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	switch a.Template {
	case "react":
		if _, err := exec.LookPath("npm"); err != nil {
			return "", fmt.Errorf("npm not found")
		}
		parent, name := filepath.Split(filepath.Clean(dest))
		cmd := exec.CommandContext(ctx, "npm", "create", "vite@latest", name, "--", "--template", "react")
		cmd.Dir = parentOrDot(parent)
		return runScaffold(cmd, dest)
	case "express":
		if _, err := exec.LookPath("npx"); err != nil {
			return "", fmt.Errorf("npx not found")
		}
		_ = os.MkdirAll(dest, 0o755)
		cmd := exec.CommandContext(ctx, "npx", "--yes", "express-generator", "--view=pug", ".")
		cmd.Dir = dest
		return runScaffold(cmd, dest)
	case "fastapi":
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return "", err
		}
		main := "from fastapi import FastAPI\n\napp = FastAPI()\n\n@app.get('/')\nasync def root():\n    return {'ok': True}\n"
		reqs := "fastapi\nuvicorn[standard]\n"
		if err := os.WriteFile(filepath.Join(dest, "main.py"), []byte(main), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dest, "requirements.txt"), []byte(reqs), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("fastapi starter at %s (pip install -r requirements.txt, uvicorn main:app)", dest), nil
	default:
		return "", fmt.Errorf("unknown template %q (react|express|fastapi)", a.Template)
	}
}

func parentOrDot(parent string) string {
	if parent == "" {
		return "."
	}
	_ = os.MkdirAll(parent, 0o755)
	return parent
}

func runScaffold(cmd *exec.Cmd, dest string) (string, error) {
	out, err := cmd.CombinedOutput()
	if len(out) > 8*1024 {
		out = append(out[:8*1024], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("scaffold failed: %v", err)
	}
	return fmt.Sprintf("scaffolded %s\n%s", dest, string(out)), nil
}
