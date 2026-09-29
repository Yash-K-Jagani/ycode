package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// Optional tool capabilities.
//
// Tool itself is four methods and stays that way: thirty built-ins, the MCP
// adapter, the plugin adapter and every SDK user implement exactly it. Anything
// richer is an *additional* interface a tool may or may not satisfy, found by
// type assertion. A tool that streams its output opts in by having a Stream
// method; a tool that is safe to delegate opts in by having Isolatable. Nothing
// has to change to keep working, and nothing has to be remembered, because the
// absence of a method is the default.
//
// This is the same pattern as http.ResponseWriter's optional interfaces
// (Flusher, Hijacker, Pusher): the base contract stays minimal and capability is
// discovered, not declared.

// Streamer is implemented by tools whose output arrives over time and is worth
// showing as it happens - a test suite, a build, a long database query.
//
// Today every such tool calls cmd.CombinedOutput and returns one string, so a
// three-minute test run shows nothing at all for three minutes. That reads as a
// hang, and users cancel work that was nearly done. With Stream, the TUI can
// render progress while the call is still in flight.
//
// Emit may be nil, which means "no progress reporting": Stream then behaves
// exactly like Run. Implementations must not assume a non-nil sink.
//
// Emit may be called from an internal goroutine and may be called
// concurrently if the tool interleaves stdout and stderr; callers of
// RunWithSink are serialised for you, but a direct caller is responsible for
// its own synchronisation. Emit must not block for long. The string returned is
// the same complete output Run would have returned, so a caller that ignores
// Emit is unaffected.
//
// A Streamer that also implements Tool must make Run and Stream produce the same
// final result; Stream is then just Run with progress on the side.
type Streamer interface {
	// Stream runs the tool, calling emit for each piece of output as it
	// arrives, and returns the complete output.
	Stream(ctx context.Context, args json.RawMessage, emit func(string)) (string, error)
}

// Isolatable is implemented by tools that are safe to run inside a subagent.
//
// A subagent gets its own context window and a narrow allow-list, and returns
// only a summary to its parent. That makes it the right way to answer "search
// the repo for every place this is called" without spending the parent's
// context on a hundred grep results - but only if the subagent cannot quietly
// rewrite the user's files. So delegation is opt-in per tool, and the built-ins
// that opt in are the ones that only read.
type Isolatable interface {
	Isolatable() bool
}

// RunWithSink runs a tool, using its streaming path when it has one.
//
// The sink is the caller's progress channel; a nil sink means the caller does
// not care, in which case Run is used directly and nothing is allocated. This
// is the single place that knows about both paths, so the agent loop, the
// headless runner and a future subagent runner all get the same behaviour.
func RunWithSink(ctx context.Context, t Tool, args json.RawMessage, sink func(string)) (string, error) {
	if sink == nil {
		return t.Run(ctx, args)
	}
	if s, ok := t.(Streamer); ok {
		return s.Stream(ctx, args, sink)
	}
	// Not a streamer: run it normally and report the result once. The sink
	// still gets something, so a caller that renders whatever arrives shows a
	// single completed card rather than nothing.
	out, err := t.Run(ctx, args)
	if out != "" {
		sink(out)
	}
	return out, err
}

// IsConcurrent reports whether a built-in tool only reads, and so may run at the
// same time as other concurrent tools.
//
// Unknown names - MCP and plugin tools - are NOT concurrent. They are arbitrary
// programs this package has never inspected, so the safe answer is that they
// run one at a time.
func IsConcurrent(name string) bool {
	if e := entryFor(strings.ToLower(name)); e != nil {
		return e.concurrent
	}
	return false
}

// IsIsolatable reports whether a tool may be handed to a subagent. The catalog
// answers for built-ins; a tool may also answer for itself, which is how an
// external tool opts in.
func IsIsolatable(name string, t Tool) bool {
	if e := entryFor(strings.ToLower(name)); e != nil {
		return e.isolatable
	}
	if t != nil {
		if i, ok := t.(Isolatable); ok {
			return i.Isolatable()
		}
	}
	return false
}

// IsStreamer reports whether a tool can report output as it happens.
func IsStreamer(t Tool) bool {
	if t == nil {
		return false
	}
	_, ok := t.(Streamer)
	return ok
}

// CanStream reports whether the named tool, once resolved, will stream.
func CanStream(reg *Registry, name string) bool {
	t, ok := reg.Get(name)
	if !ok {
		return false
	}
	return IsStreamer(t)
}

// Partition splits names into those safe to run concurrently and those that
// must run one at a time, preserving the order of each group.
//
// The agent loop uses this to run the reads of an assistant message in parallel
// while keeping every write serialised. Order is preserved in both halves so
// that the transcript still shows the calls in the order the model wrote them.
func Partition(reg *Registry, names []string) (concurrent, sequential []string) {
	for _, n := range names {
		if IsConcurrent(n) {
			concurrent = append(concurrent, n)
		} else {
			sequential = append(sequential, n)
		}
	}
	return concurrent, sequential
}
