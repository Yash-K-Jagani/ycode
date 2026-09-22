package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Type        string   `json:"type,omitempty"` // "script" (default) or "wasm"
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	Wasm        string   `json:"wasm,omitempty"` // file in plugin dir, default plugin.wasm
	Caps        []string `json:"caps,omitempty"` // wasm only, e.g. ["stdout"]
	Schema      any      `json:"schema,omitempty"`
}

func (m Manifest) kind() string {
	if strings.EqualFold(m.Type, "wasm") {
		return "wasm"
	}
	return "script"
}

func (m Manifest) wasmFile() string {
	if m.Wasm != "" {
		return m.Wasm
	}
	return "plugin.wasm"
}

func (m Manifest) wantsStdout() bool {
	for _, c := range m.Caps {
		if strings.EqualFold(c, "stdout") {
			return true
		}
	}
	return false
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
	wrt     *Runtime
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
		if err := json.Unmarshal(data, &m); err != nil || m.Name == "" {
			continue
		}
		if m.kind() == "wasm" {
			if _, err := os.Stat(filepath.Join(l.dir, e.Name(), m.wasmFile())); err != nil {
				continue
			}
		} else if m.Command == "" {
			continue
		}
		found["plugin__"+m.Name] = Plugin{Manifest: m, Dir: filepath.Join(l.dir, e.Name())}
	}
	l.mu.Lock()
	l.plugins = found
	wrt := l.wrt
	l.mu.Unlock()
	if wrt != nil {
		wrt.DropAll()
	}
}

// wasmRT lazily creates the shared WASM runtime.
func (l *Loader) wasmRT() *Runtime {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.wrt == nil {
		l.wrt = NewRuntime(context.Background())
	}
	return l.wrt
}

// Close releases the watcher and WASM runtime.
func (l *Loader) Close() {
	l.mu.Lock()
	w := l.watcher
	wrt := l.wrt
	l.watcher = nil
	l.wrt = nil
	l.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
	if wrt != nil {
		wrt.Close(context.Background())
	}
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

// Uninstall removes an installed plugin by manifest name and rescans.
func (l *Loader) Uninstall(name string) error {
	l.mu.Lock()
	dir := ""
	for _, p := range l.plugins {
		if strings.EqualFold(p.Manifest.Name, name) {
			dir = p.Dir
			break
		}
	}
	l.mu.Unlock()
	if dir == "" {
		return fmt.Errorf("plugin %q not installed", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	l.Reload()
	return nil
}

// Dirs maps installed plugin names to their directories.
func (l *Loader) Dirs() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for _, p := range l.plugins {
		out[p.Manifest.Name] = p.Dir
	}
	return out
}

// Describe lists plugins as "plugin__name (type)".
func (l *Loader) Describe() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for n, p := range l.plugins {
		out = append(out, n+" ("+p.Manifest.kind()+")")
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
	load *Loader
}

func (a *adapter) Name() string { return "plugin__" + a.plug.Manifest.Name }
func (a *adapter) Description() string {
	kind := a.plug.Manifest.kind()
	return "[plugin:" + kind + "] " + a.plug.Manifest.Description
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
	if a.plug.Manifest.kind() == "wasm" {
		return a.runWasm(ctx, args)
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

func (a *adapter) runWasm(ctx context.Context, args json.RawMessage) (string, error) {
	path := filepath.Join(a.plug.Dir, a.plug.Manifest.wasmFile())
	rt := a.load.wasmRT()
	out, err := rt.Call(ctx, path, []byte(args), a.plug.Manifest.wantsStdout())
	if err != nil {
		return "", fmt.Errorf("wasm plugin %s: %w", a.plug.Manifest.Name, err)
	}
	if len(out) > 32*1024 {
		out = append(out[:32*1024], []byte("\n…(truncated)")...)
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
		out = append(out, &adapter{plug: p, load: l})
	}
	return out
}

// Install fetches a plugin from a git URL, owner/repo shorthand, local dir,
// or a .wasm file (URL or path — manifest is synthesized).
func (l *Loader) Install(source string) (string, error) {
	if strings.HasSuffix(strings.ToLower(source), ".wasm") {
		return l.installWasm(source)
	}
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

// installWasm fetches a raw .wasm module (http(s) URL or local path) and
// synthesizes a manifest. Name comes from the file stem.
func (l *Loader) installWasm(source string) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	if stem == "" || strings.ContainsAny(stem, `/\`) {
		return "", fmt.Errorf("bad wasm name: %s", source)
	}
	dest := filepath.Join(l.dir, stem)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("plugin %q already installed", stem)
	}
	var bin []byte
	var err error
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		bin, err = fetchBytes(source)
	} else {
		bin, err = os.ReadFile(source)
	}
	if err != nil {
		return "", fmt.Errorf("fetch wasm: %w", err)
	}
	if !isWasm(bin) {
		return "", fmt.Errorf("not a wasm module (bad magic): %s", source)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, "plugin.wasm"), bin, 0o644); err != nil {
		return "", err
	}
	manifest, _ := json.Marshal(Manifest{Name: stem, Description: "wasm plugin " + stem, Type: "wasm"})
	if err := os.WriteFile(filepath.Join(dest, "plugin.json"), manifest, 0o644); err != nil {
		return "", err
	}
	l.Reload()
	return stem, nil
}

func isWasm(bin []byte) bool {
	return len(bin) >= 4 && bin[0] == 0x00 && bin[1] == 'a' && bin[2] == 's' && bin[3] == 'm'
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

// fetchBytes downloads a URL with timeout and size cap (16MB for wasm).
func fetchBytes(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
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
