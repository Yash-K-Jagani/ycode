package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"gopkg.in/yaml.v3"
)

func isWin() bool { return runtime.GOOS == "windows" }

type Event string

const (
	OnRequest  Event = "on_request"
	OnResponse Event = "on_response"
	PreTool    Event = "pre_tool"
	PostTool   Event = "post_tool"
	OnError    Event = "on_error"
)

type Entry struct {
	Command string `yaml:"command"`
}

type Hooks struct {
	Points map[Event][]Entry
}

func files() []string {
	var out []string
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, ".ycode", "hooks.yaml"))
	}
	out = append(out, filepath.Join(config.Dir(), "hooks.yaml"))
	return out
}

func Load() *Hooks {
	return LoadFiles(files())
}

func LoadFiles(paths []string) *Hooks {
	h := &Hooks{Points: map[Event][]Entry{}}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var v map[string][]Entry
		if err := yaml.Unmarshal(data, &v); err != nil {
			continue
		}
		for k, entries := range v {
			h.Points[Event(k)] = append(h.Points[Event(k)], entries...)
		}
	}
	return h
}

// Fire runs hooks best-effort; output is joined for optional display.
func (h *Hooks) Fire(ctx context.Context, e Event, fields map[string]string) string {
	var outs []string
	for _, en := range h.Points[e] {
		out, err := runHook(ctx, en.Command, e, fields)
		if err != nil {
			outs = append(outs, fmt.Sprintf("[%s hook failed: %v]", e, err))
			continue
		}
		if strings.TrimSpace(out) != "" {
			outs = append(outs, out)
		}
	}
	return strings.Join(outs, "\n")
}

// Gate runs pre_tool hooks: non-zero exit blocks the tool call.
func (h *Hooks) Gate(ctx context.Context, tool, argsFile string, fields map[string]string) error {
	f := map[string]string{"tool": tool, "args_file": argsFile}
	for k, v := range fields {
		f[k] = v
	}
	for _, en := range h.Points[PreTool] {
		out, err := runHook(ctx, en.Command, PreTool, f)
		if err != nil {
			return fmt.Errorf("pre_tool hook blocked %s: %v\n%s", tool, err, out)
		}
	}
	return nil
}

func runHook(ctx context.Context, command string, e Event, fields map[string]string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if isWin() {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	env := os.Environ()
	env = append(env, "YCODE_EVENT="+string(e))
	for k, v := range fields {
		env = append(env, "YCODE_"+strings.ToUpper(k)+"="+v)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if len(out) > 8*1024 {
		out = out[:8*1024]
	}
	return strings.TrimSpace(string(out)), err
}
