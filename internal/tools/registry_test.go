package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
)

// fakeTool is a minimal Tool used to exercise registry concurrency.
type fakeTool struct{ n string }

func (f fakeTool) Name() string        { return f.n }
func (f fakeTool) Description() string { return "fake " + f.n }
func (fakeTool) Schema() string        { return `{"type":"object"}` }
func (f fakeTool) Run(context.Context, json.RawMessage) (string, error) {
	return f.n, nil
}

// The TUI mutates the registry from the UI thread (MCP tools, plugin tools)
// while a streaming turn reads it from its own goroutine on every tool call.
// Without synchronization that is a "fatal error: concurrent map read and map
// write" — unrecoverable, and the unsaved transcript is lost with it. Run
// under -race (CI does), this fails without the mutex.
func TestRegistryIsSafeForConcurrentUse(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"read", "write", "edit", "bash", "grep"} {
		r.Add(fakeTool{n: n})
	}

	const workers = 8
	const iterations = 200
	var wg sync.WaitGroup

	// Writers: the UI thread loading MCP and plugin tools mid-turn.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				n := "mcp__srv" + strconv.Itoa(w) + "__tool" + strconv.Itoa(i%7)
				r.Add(fakeTool{n: n})
				if i%3 == 0 {
					r.Remove(n)
				}
			}
		}(w)
	}
	// Readers: the turn goroutine resolving calls and building tool docs.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = r.Names()
				if _, ok := r.Get("read"); !ok {
					t.Error("read tool disappeared")
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// The base tools must have survived the churn.
	for _, n := range []string{"read", "write", "edit", "bash", "grep"} {
		if _, ok := r.Get(n); !ok {
			t.Fatalf("%s was lost", n)
		}
	}
	if names := r.Names(); len(names) == 0 {
		t.Fatal("Names() returned nothing")
	}
}

// Names must return a snapshot, not the live slice: callers (mode allow-lists,
// system-prompt docs) hold on to it while the registry keeps changing.
func TestRegistryNamesIsASnapshot(t *testing.T) {
	r := NewRegistry()
	r.Add(fakeTool{n: "a"})
	r.Add(fakeTool{n: "b"})
	first := r.Names()
	r.Add(fakeTool{n: "c"})
	if len(first) != 2 {
		t.Fatalf("the earlier snapshot changed: %v", first)
	}
	if len(r.Names()) != 3 {
		t.Fatalf("Names() = %v, want 3 entries", r.Names())
	}
}

func TestRegistryAddIgnoresNil(t *testing.T) {
	r := NewRegistry()
	r.Add(nil)
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("a nil tool was registered: %v", got)
	}
}
