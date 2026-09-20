package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- minimal wasm assembler for ABI test modules ---

func uleb(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7F)
		n >>= 7
		if n == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func str(s string) []byte { return append(uleb(len(s)), []byte(s)...) }

func section(id byte, payload []byte) []byte {
	out := []byte{id}
	return append(append(out, uleb(len(payload))...), payload...)
}

func vec(items ...[]byte) []byte {
	out := uleb(len(items))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// buildEchoModule assembles a module implementing the ycode ABI by echoing
// input. With withWASI it also imports wasi fd_write (never called).
func buildEchoModule(withWASI bool) []byte {
	t0 := []byte{0x60, 0x01, 0x7F, 0x01, 0x7F}             // (i32)->i32
	t1 := []byte{0x60, 0x02, 0x7F, 0x7F, 0x02, 0x7F, 0x7F} // (i32,i32)->(i32,i32)
	t2 := []byte{0x60, 0x04, 0x7F, 0x7F, 0x7F, 0x7F, 0x01, 0x7F}
	t3 := []byte{0x60, 0x02, 0x7F, 0x7F, 0x00} // (i32,i32)->()
	types := vec(t0, t1, t2, t3)

	shift := 0
	var impSec []byte
	if withWASI {
		imp := append(append(str("wasi_snapshot_preview1"), str("fd_write")...), 0x00, 0x02)
		impSec = section(2, vec(imp))
		shift = 1
	}
	funcs := vec([]byte{0x00}, []byte{0x01}, []byte{0x03}) // alloc:t0 call:t1 free:t3
	memSec := section(5, vec([]byte{0x00, 0x01}))
	globSec := section(6, vec([]byte{0x7F, 0x01, 0x41, 0x80, 0x20, 0x0B}))

	exp := vec(
		append(append(str("memory"), 0x02), 0x00),
		append(append(str("ycode_alloc"), 0x00), byte(shift+0)),
		append(append(str("ycode_call"), 0x00), byte(shift+1)),
		append(append(str("ycode_free"), 0x00), byte(shift+2)),
	)

	allocBody := []byte{0x00, 0x23, 0x00, 0x23, 0x00, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x0B}
	callCode := []byte{
		0x23, 0x00, 0x22, 0x02, 0x20, 0x01, 0x6A, 0x24, 0x00,
		0x20, 0x02, 0x20, 0x00, 0x20, 0x01, 0xFC, 0x0A, 0x00, 0x00,
		0x20, 0x02, 0x20, 0x01, 0x0B,
	}
	callBody := append([]byte{0x01, 0x01, 0x7F}, callCode...)
	freeBody := []byte{0x00, 0x0B}
	codeSec := section(10, vec(
		append(uleb(len(allocBody)), allocBody...),
		append(uleb(len(callBody)), callBody...),
		append(uleb(len(freeBody)), freeBody...),
	))

	out := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	out = append(out, section(1, types)...)
	if withWASI {
		out = append(out, impSec...)
	}
	out = append(out, section(3, funcs)...)
	out = append(out, memSec...)
	out = append(out, globSec...)
	out = append(out, section(7, exp)...)
	out = append(out, codeSec...)
	return out
}

func buildMemoryOnly() []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	out = append(out, section(5, vec([]byte{0x00, 0x01}))...)
	out = append(out, section(7, vec(append(append(str("memory"), 0x02), 0x00)))...)
	return out
}

func writeMod(t *testing.T, name string, bin []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWasmEcho(t *testing.T) {
	p := writeMod(t, "echo.wasm", buildEchoModule(false))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt := NewRuntime(ctx)
	defer rt.Close(ctx)
	in := []byte(`{"path":"a.go"}`)
	out, err := rt.Call(ctx, p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("not echoed: %q", out)
	}
}

func TestWasmMissingExport(t *testing.T) {
	p := writeMod(t, "mem.wasm", buildMemoryOnly())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt := NewRuntime(ctx)
	defer rt.Close(ctx)
	if _, err := rt.Call(ctx, p, []byte(`{}`), false); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing-export error, got %v", err)
	}
}

func TestWasmWasiGate(t *testing.T) {
	p := writeMod(t, "wasi.wasm", buildEchoModule(true))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt := NewRuntime(ctx)
	defer rt.Close(ctx)
	if _, err := rt.Call(ctx, p, []byte(`{}`), false); err == nil {
		t.Fatal("expected instantiate failure without stdout cap")
	}
	out, err := rt.Call(ctx, p, []byte(`hi`), true)
	if err != nil {
		t.Fatalf("capped call failed: %v", err)
	}
	if string(out) != "hi" {
		t.Fatalf("bad echo: %q", out)
	}
}

func TestWasmAdapterEndToEnd(t *testing.T) {
	lib := t.TempDir()
	plugDir := filepath.Join(lib, "echoer")
	_ = os.MkdirAll(plugDir, 0o755)
	_ = os.WriteFile(filepath.Join(plugDir, "plugin.wasm"), buildEchoModule(false), 0o644)
	m, _ := json.Marshal(Manifest{Name: "echoer", Description: "echo", Type: "wasm"})
	_ = os.WriteFile(filepath.Join(plugDir, "plugin.json"), m, 0o644)
	l := NewLoaderAt(lib)
	defer l.Close()
	ts := l.Tools()
	if len(ts) != 1 {
		t.Fatalf("want 1 tool, got %d", len(ts))
	}
	if !strings.Contains(ts[0].Description(), "wasm") {
		t.Fatalf("want wasm tag: %q", ts[0].Description())
	}
	out, err := ts[0].Run(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"x":1}` {
		t.Fatalf("bad adapter result: %q", out)
	}
}

func TestInstallWasmFile(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "upper.wasm")
	_ = os.WriteFile(src, buildEchoModule(false), 0o644)
	l := NewLoaderAt(filepath.Join(home, "lib"))
	defer l.Close()
	name, err := l.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if name != "upper" {
		t.Fatalf("bad name %q", name)
	}
	if _, err := l.Install(src); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, err := l.Install(filepath.Join(home, "nope.wasm")); err == nil {
		t.Fatal("expected missing-file error")
	}
	bad := filepath.Join(home, "bad.wasm")
	_ = os.WriteFile(bad, []byte("not wasm"), 0o644)
	if _, err := l.Install(bad); err == nil {
		t.Fatal("expected bad-magic error")
	}
}
