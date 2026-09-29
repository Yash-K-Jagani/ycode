package agent

import (
	"context"
	"sync"

	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// Running tool calls in parallel.
//
// A model asked to inspect three files writes three <tool:read> calls in one
// message, and they were executed one after another. Each is a disk read that
// mostly waits, so the turn paid the sum of three latencies to do work that is
// three deep in I/O, and the user watched a spinner for it.
//
// Only that shape is parallelised, and the restrictions are what make it safe:
//
//   - Only tools the catalog marks concurrent. The bar there is strict - a tool
//     that only reads on *most* of its code paths is not concurrent - because
//     the classification has to hold for every call the model might make,
//     including the one it invents mid-turn.
//   - Only maximal runs of *consecutive* concurrent calls. This is what
//     preserves side-effect order. A message reading "read, write, read" runs
//     the first read, then the write, then the second read, exactly as before;
//     batching all three would have made the second read observe a file the
//     write had not produced yet, and the model would be reasoning about a
//     state that never existed.
//   - Results are returned in the order the model wrote them, whatever order
//     they finished in. The transcript is built by the caller in that order, so
//     a model reading its results back sees the same conversation it would have
//     seen before.

const (
	// maxParallelTools bounds how many calls are in flight at once. These are
	// disk reads, so the ceiling is about not exhausting file handles and not
	// starving the one sequential writer that may follow them, not about CPU.
	maxParallelTools = 6
	// A run shorter than this is not worth a goroutine each.
	minParallelRun = 2
)

// callOutcome is one call's result, in the order the model wrote it.
type callOutcome struct {
	call Call
	res  string
	err  error
	// dup is true when the call was already made earlier and is answered from
	// the cache rather than executed.
	dup bool
}

// executeCalls runs a round's tool calls and returns their outcomes in the
// original order.
//
// cached is read here to decide what to skip, and written by the caller after
// execution, so it is never touched from more than one goroutine.
func executeCalls(ctx context.Context, reg *tools.Registry, allow map[string]bool, hk *hooks.Hooks, mode string, calls []Call, cached map[string]string) []callOutcome {
	out := make([]callOutcome, len(calls))
	i := 0
	for i < len(calls) {
		// How long is the run of concurrent-safe calls here? Asked of the
		// registry rather than the catalog, because a plugin or a test tool
		// declares its own capability.
		n := 0
		for i+n < len(calls) && reg.Concurrent(calls[i+n].Name) {
			n++
		}
		if n < minParallelRun {
			// Zero means this call is not concurrent, one means a run too
			// short to be worth the goroutine. Either way, inline.
			out[i] = runOne(ctx, reg, allow, hk, mode, calls[i], cached)
			i++
			continue
		}
		runParallel(ctx, reg, allow, hk, mode, calls[i:i+n], cached, out[i:i+n])
		i += n
	}
	return out
}

// runOne executes a single call, or answers it from the cache if this exact
// call has already been made.
func runOne(ctx context.Context, reg *tools.Registry, allow map[string]bool, hk *hooks.Hooks, mode string, c Call, cached map[string]string) callOutcome {
	if _, dup := cached[callKey(c)]; dup {
		return callOutcome{call: c, dup: true}
	}
	res, err := execCall(ctx, reg, allow, hk, mode, c)
	return callOutcome{call: c, res: res, err: err}
}

// runParallel executes a run of concurrent-safe calls, writing the outcomes
// into dst at the same indices so the order is preserved.
func runParallel(ctx context.Context, reg *tools.Registry, allow map[string]bool, hk *hooks.Hooks, mode string, calls []Call, cached map[string]string, dst []callOutcome) {
	var wg sync.WaitGroup
	// A plain channel as a semaphore, rather than a worker pool: a worker pool
	// needs a queue and a closer, and this is a bounded number of calls.
	sem := make(chan struct{}, maxParallelTools)
	// Keys already seen in this run, so the second of two identical calls is
	// answered from the first's result rather than being executed twice.
	// Within one run this cannot be shared across goroutines, so the decision
	// is made here, single threaded, and the repeats are resolved afterwards.
	seen := make(map[string]bool, len(calls))

	for i, c := range calls {
		key := callKey(c)
		if seen[key] {
			// A repeat inside the same run. The first one's result will be in
			// cached by the time the caller processes this, so leave it marked
			// and let the caller resolve it.
			dst[i] = callOutcome{call: c, dup: true}
			continue
		}
		seen[key] = true
		if _, dup := cached[key]; dup {
			dst[i] = callOutcome{call: c, dup: true}
			continue
		}
		wg.Add(1)
		go func(i int, c Call) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := execCall(ctx, reg, allow, hk, mode, c)
			// Each goroutine writes its own index, so there is no shared
			// state here at all.
			dst[i] = callOutcome{call: c, res: res, err: err}
		}(i, c)
	}
	wg.Wait()
}

func callKey(c Call) string { return c.Name + "\x00" + string(c.Args) }
