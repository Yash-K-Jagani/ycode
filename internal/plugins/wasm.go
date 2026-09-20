package plugins

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ABI (documented in docs/plugins.md):
//   - memory: little-endian linear memory, UTF-8 JSON payloads.
//   - ycode_alloc(size: i32) -> i32 ptr   (required)
//   - ycode_call(in_ptr: i32, in_len: i32) -> (out_ptr: i32, out_len: i32) (required)
//   - ycode_free(ptr: i32, len: i32)       (optional, best-effort)
// Input is the tool-args JSON; output is the result string (or JSON {"error": ...}).

const callTimeout = 60 * time.Second

// Runtime caches compiled modules and isolates instances per call.
// Two underlying runtimes exist: plain (no WASI imports resolvable) and
// wasi (stdout only, to io.Discard) for manifests granting the stdout cap.
type Runtime struct {
	mu       sync.Mutex
	plain    wazero.Runtime
	withWASI wazero.Runtime
	compiled map[string]compiled
}

type compiled struct {
	mod wazero.CompiledModule
}

func NewRuntime(ctx context.Context) *Runtime {
	plain := wazero.NewRuntime(ctx)
	wasiRt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, wasiRt)
	return &Runtime{plain: plain, withWASI: wasiRt, compiled: map[string]compiled{}}
}

func (r *Runtime) Close(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.plain.CloseWithExitCode(ctx, 0)
	_ = r.withWASI.CloseWithExitCode(ctx, 0)
	r.compiled = map[string]compiled{}
}

// Drop forgets a cached module (called on hot-reload).
func (r *Runtime) Drop(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.compiled, path)
}

// DropAll forgets all cached modules.
func (r *Runtime) DropAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compiled = map[string]compiled{}
}

func (r *Runtime) getCompiled(ctx context.Context, path string) (wazero.CompiledModule, wazero.Runtime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.compiled[path]; ok {
		return c.mod, r.plain, nil
	}
	bin, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// Compiled against plain; the WASI variant recompiles on demand in Call.
	cm, err := r.plain.CompileModule(ctx, bin)
	if err != nil {
		return nil, nil, fmt.Errorf("compile %s: %w", path, err)
	}
	r.compiled[path] = compiled{mod: cm}
	return cm, r.plain, nil
}

// Call executes ycode_call in a fresh instance. stdoutCap gates WASI.
func (r *Runtime) Call(ctx context.Context, path string, input []byte, stdoutCap bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	cm, _, err := r.getCompiled(ctx, path)
	if err != nil {
		return nil, err
	}
	rt := r.plain
	if stdoutCap {
		// Recompile for the WASI runtime (imports differ).
		bin, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		cm, err = r.withWASI.CompileModule(ctx, bin)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", path, err)
		}
		rt = r.withWASI
	}
	modCfg := wazero.NewModuleConfig().WithStdout(io.Discard).WithStderr(io.Discard)
	mod, err := rt.InstantiateModule(ctx, cm, modCfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate %s: %w (needs stdout cap? manifest caps)", path, err)
	}
	defer func() { _ = mod.CloseWithExitCode(ctx, 0) }()

	alloc := mod.ExportedFunction("ycode_alloc")
	call := mod.ExportedFunction("ycode_call")
	if alloc == nil || call == nil {
		return nil, fmt.Errorf("%s: missing ycode_alloc/ycode_call exports", path)
	}
	mem := mod.Memory()
	res, err := alloc.Call(ctx, uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("alloc: %w", err)
	}
	inPtr := uint32(res[0])
	if !mem.Write(inPtr, input) {
		return nil, fmt.Errorf("write input: out of bounds")
	}
	out, err := call.Call(ctx, uint64(inPtr), uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("ycode_call: %w", err)
	}
	if len(out) != 2 {
		return nil, fmt.Errorf("ycode_call must return (ptr, len)")
	}
	data, ok := mem.Read(uint32(out[0]), uint32(out[1]))
	if !ok {
		return nil, fmt.Errorf("read output: out of bounds")
	}
	cp := append([]byte(nil), data...)
	if free := mod.ExportedFunction("ycode_free"); free != nil {
		_, _ = free.Call(ctx, out[0], out[1])
	}
	return cp, nil
}
