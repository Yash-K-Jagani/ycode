package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type runSpec struct {
	ext     string
	check   []string
	build   func(file string) []string
	timeout time.Duration
}

func runSpecs() map[string]runSpec {
	py := []string{"python", "-u"}
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("python3"); err == nil {
			py = []string{"python3", "-u"}
		}
	}
	return map[string]runSpec{
		"python":     {ext: ".py", build: func(f string) []string { return append(append([]string{}, py...), f) }},
		"javascript": {ext: ".js", check: []string{"node"}, build: func(f string) []string { return []string{"node", f} }},
		"js":         {ext: ".js", check: []string{"node"}, build: func(f string) []string { return []string{"node", f} }},
		"typescript": {ext: ".ts", check: []string{"deno", "bun"}, build: denoBun},
		"ts":         {ext: ".ts", check: []string{"deno", "bun"}, build: denoBun},
		"go":         {ext: ".go", check: []string{"go"}, build: func(f string) []string { return []string{"go", "run", f} }},
		"bash":       {ext: ".sh", build: func(f string) []string { return []string{"sh", f} }},
		"sh":         {ext: ".sh", build: func(f string) []string { return []string{"sh", f} }},
		"powershell": {ext: ".ps1", build: func(f string) []string { return []string{"powershell", "-NoProfile", "-NonInteractive", "-File", f} }},
		"ps1":        {ext: ".ps1", build: func(f string) []string { return []string{"powershell", "-NoProfile", "-NonInteractive", "-File", f} }},
		"ruby":       {ext: ".rb", check: []string{"ruby"}, build: func(f string) []string { return []string{"ruby", f} }},
		"php":        {ext: ".php", check: []string{"php"}, build: func(f string) []string { return []string{"php", f} }},
		"java":       {ext: ".java", check: []string{"java"}, build: func(f string) []string { return []string{"java", f} }, timeout: 2 * time.Minute},
		"rust":       {ext: ".rs", check: []string{"rustc"}, build: nil, timeout: 3 * time.Minute},
	}
}

func denoBun(f string) []string {
	if _, err := exec.LookPath("deno"); err == nil {
		return []string{"deno", "run", "--allow-all", f}
	}
	return []string{"bun", f}
}

type RunTool struct{ Workdir string }

func (RunTool) Name() string { return "run" }
func (RunTool) Description() string {
	return "Run code and return its output. Args: language (python|javascript|typescript|go|bash|powershell|ruby|php|java|rust), code (inline, optional), path (existing file, optional — one required). 60s timeout."
}
func (RunTool) Schema() string {
	return `{"type":"object","properties":{"language":{"type":"string"},"code":{"type":"string"},"path":{"type":"string"}}}`
}

func (t *RunTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return t.run(ctx, args, nil)
}

// Stream runs the tool, reporting output as it is produced.
//
// Running a script is one of the places where silence looks worst: the
// interpreter can be compiling, downloading, or waiting on something the user
// cannot see, for minutes. execStream gives the same retained output Run would
// have returned, so a caller that ignores the sink is unaffected.
func (t *RunTool) Stream(ctx context.Context, args json.RawMessage, emit func(string)) (string, error) {
	return t.run(ctx, args, emit)
}

func (t *RunTool) run(ctx context.Context, args json.RawMessage, emit func(string)) (string, error) {
	var a struct {
		Language string `json:"language"`
		Code     string `json:"code"`
		Path     string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("run is blocked in read-only mode")
	}
	lang := strings.ToLower(strings.TrimSpace(a.Language))
	spec, ok := runSpecs()[lang]
	if !ok {
		return "", fmt.Errorf("unsupported language %q (python|javascript|typescript|go|bash|powershell|ruby|php|java|rust)", a.Language)
	}
	for _, c := range bashDeny {
		if strings.Contains(strings.ToLower(a.Code), strings.ToLower(c)) {
			return "", fmt.Errorf("blocked by safety denylist: %q", c)
		}
	}
	file := ""
	if a.Code != "" {
		dir, err := os.MkdirTemp("", "ycode-run-*")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		file = filepath.Join(dir, "main"+spec.ext)
		if err := os.WriteFile(file, []byte(a.Code), 0o644); err != nil {
			return "", err
		}
	} else if a.Path != "" {
		file = resolve(t.Workdir, a.Path)
	} else {
		return "", fmt.Errorf("provide code or path")
	}
	for _, c := range spec.check {
		if _, err := exec.LookPath(c); err != nil {
			return "", fmt.Errorf("%s runtime not found (%s) — install it or pick another language", lang, c)
		}
	}
	timeout := 60 * time.Second
	if spec.timeout > 0 {
		timeout = spec.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if lang == "rust" {
		return runRust(ctx, t.Workdir, file, emit)
	}
	argv := spec.build(file)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if t.Workdir != "" {
		cmd.Dir = t.Workdir
	}
	res := execStream(ctx, cmd, emit, maxOutBytes)
	if ctx.Err() == context.DeadlineExceeded {
		return string(res.out), fmt.Errorf("timeout after %s", timeout)
	}
	if res.err != nil {
		return string(res.out), fmt.Errorf("exit error: %v", res.err)
	}
	if len(res.out) == 0 {
		return "(no output)", nil
	}
	return string(res.out), nil
}

// runRust compiles and runs a Rust file. Both phases stream: the compile is the
// slow one on a cold target directory, and rustc's diagnostics are the output
// people actually need to see.
func runRust(ctx context.Context, workdir, file string, emit func(string)) (string, error) {
	dir, err := os.MkdirTemp("", "ycode-rust-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, "prog")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(ctx, "rustc", "-O", "-o", bin, file)
	if res := execStream(ctx, build, emit, maxOutBytes); res.err != nil {
		return string(res.out), fmt.Errorf("compile error: %v", res.err)
	}
	run := exec.CommandContext(ctx, bin)
	if workdir != "" {
		run.Dir = workdir
	}
	res := execStream(ctx, run, emit, maxOutBytes)
	if res.err != nil {
		return string(res.out), fmt.Errorf("exit error: %v", res.err)
	}
	if len(res.out) == 0 {
		return "(no output)", nil
	}
	return string(res.out), nil
}
