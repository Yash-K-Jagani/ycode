// Package toolwire builds the function-tool payload both streaming providers
// send, and caches it.
//
// It is a leaf package because internal/providers imports both ollama and
// openaicompat, so neither can host shared code for the other. Same reason
// httpx ended up owning the shared line reader.
package toolwire

import (
	"sync"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// cacheMax bounds the memo.
//
// Tool specs come from the registry and change only when a tool is added or
// removed, so in practice this holds one entry. The bound exists so a caller
// building a distinct spec list per request cannot grow it without limit.
const cacheMax = 8

// entry pairs a payload with the specs that produced it.
type entry struct {
	specs []apitypes.ToolSpec
	tools []map[string]any
}

var (
	mu    sync.Mutex
	cache []entry
)

// List returns the "tools" array for specs.
//
// The result is shared and must be treated as read-only. Both callers copy it
// into a fresh request map and marshal it, so nothing mutates it - but that is a
// promise this function's callers have to keep, which is why it is documented
// rather than defended with a deep copy on every call.
//
// The cache exists because the spec list is rebuilt into maps on every request
// and nothing about it changes between turns: measured at 32us and 21.9KB of
// garbage for the 30-tool default registry, per request.
func List(specs []apitypes.ToolSpec) []map[string]any {
	if len(specs) == 0 {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	for i := range cache {
		if sameSpecs(cache[i].specs, specs) {
			return cache[i].tools
		}
	}
	tools := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		fn := map[string]any{"name": s.Name}
		if s.Description != "" {
			fn["description"] = s.Description
		}
		if s.Schema != "" {
			fn["parameters"] = s.Schema
		}
		tools = append(tools, map[string]any{"type": "function", "function": fn})
	}
	if len(cache) >= cacheMax {
		cache = cache[:0]
	}
	// Copied, because the caller owns its slice and may reuse or mutate it.
	cache = append(cache, entry{specs: append([]apitypes.ToolSpec(nil), specs...), tools: tools})
	return tools
}

// sameSpecs compares field by field rather than hashing.
//
// This was a hash first, and the boundary case broke it: with a separator only
// between specs and not between a spec's own fields, {Name:"ab",
// Description:"c"} and {Name:"a", Description:"bc"} hashed identically, so the
// second would have been served the first's tools - the model silently calling
// tools that do not exist. Comparing costs nothing, since ToolSpec is three
// strings and so compares with ==, and it cannot collide at all.
func sameSpecs(a, b []apitypes.ToolSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
