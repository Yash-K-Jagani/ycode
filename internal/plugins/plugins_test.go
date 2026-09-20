package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

func writePlugin(t *testing.T, dir, name, desc, command string) {
	t.Helper()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	m := map[string]string{"name": name, "description": desc, "command": command}
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(d, "plugin.json"), b, 0o644)
}

func TestLoadAndRun(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "hello", "says hi", "echo hi-from-plugin")
	l := NewLoaderAt(dir)
	if names := l.Names(); len(names) != 1 || names[0] != "plugin__hello" {
		t.Fatalf("bad names: %v", names)
	}
	ts := l.Tools()
	if len(ts) != 1 {
		t.Fatalf("bad tools: %d", len(ts))
	}
	out, err := ts[0].Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out == "" || (len(out) > 0 && out != "hi-from-plugin\r\n" && out != "hi-from-plugin\n") {
		t.Logf("output: %q", out)
	}
	ro := tools.WithReadOnly(context.Background())
	if _, err := ts[0].Run(ro, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected read-only block")
	}
}

func TestHotReload(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "a", "A", "echo a")
	l := NewLoaderAt(dir)
	stop := l.Watch()
	defer stop()
	if len(l.Names()) != 1 {
		t.Fatal("expected 1")
	}
	writePlugin(t, dir, "b", "B", "echo b")
	time.Sleep(1200 * time.Millisecond)
	if len(l.Names()) != 2 {
		t.Fatalf("watcher did not reload: %v", l.Names())
	}
}

func TestBadManifestSkipped(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "bad")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "plugin.json"), []byte("{nope"), 0o644)
	l := NewLoaderAt(dir)
	if len(l.Names()) != 0 {
		t.Fatal("bad manifest should be skipped")
	}
}

func TestInstallLocalAndShorthand(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcplug")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"name":"p","description":"d","command":"echo hi"}`), 0o644)
	l := NewLoaderAt(filepath.Join(dir, "lib"))
	name, err := l.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if name != "srcplug" || len(l.Names()) != 1 {
		t.Fatalf("bad install: %q %v", name, l.Names())
	}
	if _, err := l.Install(src); err == nil {
		t.Fatal("expected duplicate error")
	}
	if !isShorthand("owner/repo") || isShorthand("https://x/y") || isShorthand("noslash") {
		t.Fatal("shorthand detect wrong")
	}
}
