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

	"github.com/Yash-K-Jagani/ycode/internal/lang"
)

type TestGenTool struct{ Workdir string }

func (TestGenTool) Name() string { return "testgen" }
func (TestGenTool) Description() string {
	return "Run the project's test suite (Go/Rust/Node/Deno/Bun/Java/C#/PHP/Ruby/Python auto-detected). Args: path (file or dir, required), run (optional test-name filter), args (optional extra CLI args)."
}
func (TestGenTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"run":{"type":"string"},"args":{"type":"string"}}}`
}

func (t *TestGenTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Run  string `json:"run"`
		Args string `json:"args"`
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
	proj, ok := lang.Detect(dir)
	if !ok {
		return "", fmt.Errorf("no supported test setup found above %s (go/rust/node/deno/bun/java/c#/php/ruby/python)", dir)
	}
	argv := append(append([]string{}, proj.TestCmd...), filterArgs(proj.Language, a.Run)...)
	if a.Args != "" {
		argv = append(argv, strings.Fields(a.Args)...)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = proj.Root
	out, err := cmd.CombinedOutput()
	if len(out) > maxOutBytes {
		out = append(out[:maxOutBytes], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("tests failed: %v", err)
	}
	return string(out), nil
}

// filterArgs converts a test-name filter into runner-native flags.
func filterArgs(language, run string) []string {
	if run == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(language, "Go"):
		return []string{"-run", run}
	case strings.HasPrefix(language, "Rust"):
		return []string{run}
	case strings.Contains(language, "Deno"):
		return []string{"--filter", run}
	case strings.Contains(language, "Bun"):
		return []string{"-t", run}
	case strings.Contains(language, "Python"):
		return []string{"-k", run}
	case strings.Contains(language, "PHP"):
		return []string{"--filter", run}
	case strings.Contains(language, "Ruby"):
		return []string{"-n", run}
	default:
		return []string{run}
	}
}
