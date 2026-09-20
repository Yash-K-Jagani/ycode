# Plugins

Two kinds live side by side in `~/.ycode/plugins/<name>/`, both exposed as
`plugin__<name>` tools in build mode (blocked in read-only mode):

## Script plugins (`plugin.json`)

```json
{
  "name": "hello",
  "description": "says hi",
  "command": "echo hi",
  "args": [],
  "schema": {"type": "object"}
}
```

- `command` runs in the plugin dir (shell on Linux/macOS, PowerShell on Windows).
- Model input arrives in `$YCODE_PLUGIN_ARGS` (JSON). stdout is the result (32KB cap, 90s timeout).

## WASM plugins (`plugin.wasm` + manifest)

```json
{
  "name": "upper",
  "description": "uppercases input",
  "type": "wasm",
  "caps": ["stdout"],
  "schema": {"type": "object"}
}
```

- Runs **in-process** via wazero (pure Go, keeps the single-binary promise). No
  subprocess, no shell — and by default **no WASI at all**: filesystem and
  network are unreachable. Grant `"caps": ["stdout"]` only if the module needs
  WASI `fd_write` (output is discarded; results travel via return values).
- 60s wall-clock timeout per call, 32KB result cap, fresh isolated instance
  every call, compiled modules cached (dropped on reload).
- Install: `/plugins install <git-url|owner/repo|local-dir|file-or-url.wasm>`
  (`.wasm` sources get a synthesized manifest; magic bytes verified).

### WASM ABI

Linear memory, little-endian, UTF-8 JSON. Required exports:

- `ycode_alloc(size: i32) -> i32` — bump-allocate, return pointer.
- `ycode_call(in_ptr: i32, in_len: i32) -> (out_ptr: i32, out_len: i32)` —
  read the args JSON at input, write the result string, return its span.
- `ycode_free(ptr: i32, len: i32)` — optional, called best-effort.

Input is the tool-args JSON object; return the result string (or a JSON
`{"error": "..."}` payload — the harness surfaces returned text as-is).

TinyGo sketch:

```go
package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

var heap = make([]byte, 0, 65536)

//go:export ycode_alloc
func alloc(size uint32) uint32 {
	p := uint32(len(heap))
	heap = append(heap, make([]byte, size)...)
	return p
}

//go:export ycode_call
func call(inPtr, inLen uint32) (uint32, uint32) {
	in := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(inPtr))), inLen))
	var args map[string]any
	_ = json.Unmarshal([]byte(in), &args)
	_ = args
	out := []byte(strings.ToUpper(in))
	p := alloc(uint32(len(out)))
	copy(heap[p:], out)
	return p, uint32(len(out))
}

func main() {}
```

Build with `tinygo build -o plugin.wasm -target=wasi .` (plain `wasm` target
also works if the module needs no WASI imports).

## Management

- `/plugins` lists with types, `/plugins reload` rescans (hot-reload),
  `/plugins install <src>` fetches.
- See also: MCP servers (`/mcps`) for richer integrations.

