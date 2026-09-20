package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type TestGenTool struct{ Workdir string }

func (TestGenTool) Name() string { return "testgen" }
func (TestGenTool) Description() string {
	return "Run the project's test suite for a file's package (Go/Python/Node auto-detected). Args: path (file or dir, required)."
}
func (TestGenTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`
}

func (t *TestGenTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("testgen is blocked in read-only mode")
	}
	p := resolve(t.Workdir, a.Path)
	if p == "" {
		p = t.Workdir
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	dir := p
	if !fi.IsDir() {
		dir = filepath.Dir(p)
	}
	kind, root := detectProject(dir)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch kind {
	case "go":
		cmd = exec.CommandContext(ctx, "go", "test", "./...")
	case "node":
		cmd = exec.CommandContext(ctx, "npm", "test", "--silent")
	case "python":
		cmd = exec.CommandContext(ctx, "python", "-m", "pytest", "-q")
	default:
		return "", fmt.Errorf("no supported test setup found (go.mod/package.json/pytest) above %s", dir)
	}
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if len(out) > maxOutBytes {
		out = append(out[:maxOutBytes], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("tests failed: %v", err)
	}
	return string(out), nil
}

func detectProject(dir string) (kind, root string) {
	d := dir
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return "go", d
		}
		if _, err := os.Stat(filepath.Join(d, "package.json")); err == nil {
			return "node", d
		}
		if hasPy(d) {
			return "python", d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", dir
		}
		d = parent
	}
}

func hasPy(d string) bool {
	for _, f := range []string{"pytest.ini", "pyproject.toml", "setup.py", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(d, f)); err == nil {
			return true
		}
	}
	if strings.HasSuffix(d, ".py") {
		return true
	}
	return false
}
