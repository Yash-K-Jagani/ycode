package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/fsnotify/fsnotify"
)

func isWin() bool { return runtime.GOOS == "windows" }

type Manifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Schema      any      `json:"schema,omitempty"`
}

type Plugin struct {
	Manifest Manifest
	Dir      string
}

type Loader struct {
	mu      sync.Mutex
	dir     string
	plugins map[string]Plugin
	watcher *fsnotify.Watcher
}

func defaultDir() string { return filepath.Join(config.Dir(), "plugins") }

func NewLoader() *Loader { return NewLoaderAt(defaultDir()) }

func NewLoaderAt(dir string) *Loader {
	l := &Loader{dir: dir, plugins: map[string]Plugin{}}
	l.Reload()
	return l
}

func (l *Loader) Reload() {
	found := map[string]Plugin{}
	entries, _ := os.ReadDir(l.dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.dir, e.Name(), "plugin.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil || m.Name == "" || m.Command == "" {
			continue
		}
		found["plugin__"+m.Name] = Plugin{Manifest: m, Dir: filepath.Join(l.dir, e.Name())}
	}
	l.mu.Lock()
	l.plugins = found
	l.mu.Unlock()
}

func (l *Loader) Names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for n := range l.plugins {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Watch reloads on file changes (debounced). Stop with the returned func.
func (l *Loader) Watch() (stop func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return func() {}
	}
	l.watcher = w
	_ = w.Add(l.dir)
	done := make(chan struct{})
	go func() {
		var timer *time.Timer
		for {
			select {
			case <-done:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if strings.Contains(ev.Name, "plugin.json") || ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(300*time.Millisecond, l.Reload)
				}
			case <-w.Errors:
			}
		}
	}()
	return func() { close(done); _ = w.Close() }
}

type adapter struct {
	plug Plugin
}

func (a *adapter) Name() string { return "plugin__" + a.plug.Manifest.Name }
func (a *adapter) Description() string {
	return "[plugin] " + a.plug.Manifest.Description
}
func (a *adapter) Schema() string {
	b, _ := json.Marshal(a.plug.Manifest.Schema)
	if len(b) == 0 || string(b) == "null" {
		return `{"type":"object"}`
	}
	return string(b)
}
func (a *adapter) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if tools.IsReadOnly(ctx) {
		return "", fmt.Errorf("plugins are blocked in read-only mode")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if isWin() {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", a.plug.Manifest.Command+" "+strings.Join(quoteArgs(a.plug.Manifest.Args), " "))
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.plug.Manifest.Command+" "+strings.Join(quoteArgs(a.plug.Manifest.Args), " "))
	}
	cmd.Dir = a.plug.Dir
	cmd.Env = append(os.Environ(), "YCODE_PLUGIN_ARGS="+string(args))
	out, err := cmd.CombinedOutput()
	if len(out) > 32*1024 {
		out = append(out[:32*1024], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("plugin %s failed: %v", a.plug.Manifest.Name, err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "(no output)", nil
	}
	return string(out), nil
}

func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			out[i] = "\"" + strings.ReplaceAll(a, "\"", "\\\"") + "\""
		} else {
			out[i] = a
		}
	}
	return out
}

func (l *Loader) Tools() []tools.Tool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []tools.Tool
	for _, p := range l.plugins {
		p := p
		out = append(out, &adapter{plug: p})
	}
	return out
}

// Install fetches a plugin from a git URL, owner/repo shorthand, or local dir.
func (l *Loader) Install(source string) (string, error) {
	if isShorthand(source) {
		source = "https://github.com/" + source + ".git"
	}
	if isPluginURL(source) {
		base := source[strings.LastIndex(source, "/")+1:]
		name := strings.TrimSuffix(base, ".git")
		dest := filepath.Join(l.dir, name)
		if _, err := os.Stat(dest); err == nil {
			return "", fmt.Errorf("plugin %q already installed", name)
		}
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		cmd := exec.Command("git", "clone", "--depth=1", source, dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("clone failed: %v\n%s", err, out)
		}
		l.Reload()
		return name, nil
	}
	fi, err := os.Stat(source)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("not a plugin dir or URL: %s", source)
	}
	name := filepath.Base(source)
	dest := filepath.Join(l.dir, name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("plugin %q already installed", name)
	}
	if err := copyPluginDir(source, dest); err != nil {
		return "", err
	}
	l.Reload()
	return name, nil
}

func isPluginURL(s string) bool {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "git@") {
		for _, h := range []string{"github.com", "gitlab.com"} {
			if strings.Contains(s, h) {
				return true
			}
		}
	}
	return false
}

func isShorthand(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") || !strings.Contains(s, "/") {
		return false
	}
	parts := strings.Split(s, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func copyPluginDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
