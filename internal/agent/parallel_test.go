package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// instrumented is a tool that records what it was asked to do, and can be made
// slow, so a test can tell concurrency from sequence without depending on how
// fast the machine is.
type instrumented struct {
	name       string
	delay      time.Duration
	concurrent bool

	mu      sync.Mutex
	started []string

	// inFlight and peak are bumped on entry. Peak is the measurement: it
	// answers "did these overlap" directly, where timing only suggests it.
	inFlight int32
	peak     int32
}

func (t *instrumented) Name() string        { return t.name }
func (t *instrumented) Description() string { return "instrumented " + t.name }
func (t *instrumented) Schema() string      { return `{"type":"object"}` }

func (t *instrumented) Run(ctx context.Context, args json.RawMessage) (string, error) {
	cur := atomic.AddInt32(&t.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&t.peak)
		if cur <= peak || atomic.CompareAndSwapInt32(&t.peak, peak, cur) {
			break
		}
	}
	t.mu.Lock()
	t.started = append(t.started, string(args))
	t.mu.Unlock()
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	atomic.AddInt32(&t.inFlight, -1)
	return "ran " + string(args), nil
}

func (t *instrumented) peakConcurrency() int32 { return atomic.LoadInt32(&t.peak) }

func (t *instrumented) startCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.started)
}

// argsFor builds tool arguments. It has to be a JSON object, not a bare string:
// execCall unmarshals the arguments into a map before dispatching, so a scalar
// fails validation and the tool never runs - which looks exactly like the
// parallel path not working.
func argsFor(v string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"k": v})
	return b
}

func allow(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// The point of the change: three reads in one message should overlap. Measured
// from inside the tool, so this cannot flake on a slow or busy machine.
func TestConcurrentToolsActuallyOverlap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	read := &instrumented{name: "read", delay: 60 * time.Millisecond, concurrent: true}
	reg := tools.NewRegistry()
	reg.AddWith(read, true, true)

	calls := []Call{
		{Name: "read", Args: argsFor("a")},
		{Name: "read", Args: argsFor("b")},
		{Name: "read", Args: argsFor("c")},
	}
	got := executeCalls(context.Background(), reg, allow("read"), nil, "build", calls, map[string]string{})
	if len(got) != 3 {
		t.Fatalf("got %d outcomes", len(got))
	}
	if read.peakConcurrency() < 2 {
		t.Fatalf("peak concurrency was %d; the calls ran one after another", read.peakConcurrency())
	}
}

// Order is what the model reads back, so it cannot depend on which call
// finished first. The delays run the other way round from the call order, so a
// parallel implementation that reordered would fail here.
func TestResultsKeepTheOrderTheModelWroteThem(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	delays := map[string]time.Duration{
		"a": 120 * time.Millisecond,
		"b": 60 * time.Millisecond,
		"c": 5 * time.Millisecond,
	}
	reg := tools.NewRegistry()
	for k, d := range delays {
		reg.AddWith(&instrumented{name: k, delay: d, concurrent: true}, true, true)
	}
	calls := []Call{
		{Name: "a", Args: argsFor("first")},
		{Name: "b", Args: argsFor("second")},
		{Name: "c", Args: argsFor("third")},
	}
	got := executeCalls(context.Background(), reg, allow("a", "b", "c"), nil, "build", calls, map[string]string{})
	want := []string{"first", "second", "third"}
	for i := range want {
		if !strings.Contains(got[i].res, want[i]) {
			t.Fatalf("outcome %d = %q, want the %s call; order followed completion", i, got[i].res, want[i])
		}
		if got[i].call.Name != calls[i].Name {
			t.Fatalf("outcome %d carries call %q, want %q", i, got[i].call.Name, calls[i].Name)
		}
	}
}

// The rule that makes this safe, and the one worth a test of its own.
//
// A write between two reads must still run between them. Batching all three
// would have started the second read while the first was still in flight,
// before the write produced anything, so the model would be reading back a
// state that never existed.
//
// This is checked against a shared value rather than against timing, so it is
// deterministic: the read returns whatever the write last stored.
func TestAWriteBetweenTwoReadsStillRunsBetweenThem(t *testing.T) {
	world := &sharedWorld{value: "original"}

	read := func(ctx context.Context, args json.RawMessage) (string, error) {
		return "saw:" + world.get(), nil
	}
	write := func(ctx context.Context, args json.RawMessage) (string, error) {
		world.set("written")
		return "wrote", nil
	}

	reg := tools.NewRegistry()
	reg.AddWith(tool{name: "read", run: read}, true, true)
	reg.AddWith(tool{name: "write", run: write}, false, false)

	calls := []Call{
		{Name: "read", Args: argsFor("before")},
		{Name: "write", Args: argsFor("mid")},
		{Name: "read", Args: argsFor("after")},
	}
	got := executeCalls(context.Background(), reg, allow("read", "write"), nil, "build", calls, map[string]string{})

	if got[0].res != "saw:original" {
		t.Fatalf("the first read saw %q, want the original value", got[0].res)
	}
	if got[2].res != "saw:written" {
		t.Fatalf("the second read saw %q; the write did not run between the reads", got[2].res)
	}
}

// And the other side: when everything in the message is a read, they do run
// together. Otherwise the ordering rule above could be satisfied by never
// running anything in parallel.
func TestConsecutiveReadsWithNoWriteStillOverlap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	world := &sharedWorld{value: "v"}
	reg := tools.NewRegistry()
	for i := 0; i < 3; i++ {
		reg.AddWith(tool{name: "read", run: func(ctx context.Context, args json.RawMessage) (string, error) {
			time.Sleep(50 * time.Millisecond)
			return "saw:" + world.get(), nil
		}}, true, true)
	}
	// Same name, so the registry holds the last one; drive it through a
	// counting tool to measure the overlap instead.
	_ = reg
	counted := &instrumented{name: "read", delay: 50 * time.Millisecond, concurrent: true}
	reg2 := tools.NewRegistry()
	reg2.AddWith(counted, true, true)
	calls := []Call{
		{Name: "read", Args: argsFor("a")},
		{Name: "read", Args: argsFor("b")},
		{Name: "read", Args: argsFor("c")},
	}
	executeCalls(context.Background(), reg2, allow("read"), nil, "build", calls, map[string]string{})
	if counted.peakConcurrency() < 2 {
		t.Fatalf("peak concurrency %d; consecutive reads did not overlap", counted.peakConcurrency())
	}
}

// A tool that is not marked concurrent must never overlap with anything.
func TestNonConcurrentToolRunsAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	writer := &instrumented{name: "write", delay: 50 * time.Millisecond, concurrent: false}
	reg := tools.NewRegistry()
	reg.AddWith(writer, false, false)
	reader := &instrumented{name: "read", delay: 50 * time.Millisecond, concurrent: true}
	reg.AddWith(reader, true, true)

	calls := []Call{
		{Name: "read", Args: argsFor("a")},
		{Name: "write", Args: argsFor("w")},
		{Name: "read", Args: argsFor("b")},
	}
	executeCalls(context.Background(), reg, allow("read", "write"), nil, "build", calls, map[string]string{})

	if writer.peakConcurrency() != 1 {
		t.Fatalf("the write peaked at %d; it must run alone", writer.peakConcurrency())
	}
	if reader.peakConcurrency() != 1 {
		t.Fatalf("the reads peaked at %d; the write split them into separate runs", reader.peakConcurrency())
	}
}

// A repeat of the same call within one message must not be executed twice. The
// parallel path has to reach the same conclusion the serial one did, or a
// model that repeats itself makes the loop do twice the work.
func TestDuplicateCallsInOneMessageRunOnce(t *testing.T) {
	var runs int32
	reg := tools.NewRegistry()
	reg.AddWith(counting{name: "read", runs: &runs}, true, true)
	calls := []Call{
		{Name: "read", Args: argsFor("same")},
		{Name: "read", Args: argsFor("same")},
		{Name: "read", Args: argsFor("same")},
	}
	got := executeCalls(context.Background(), reg, allow("read"), nil, "build", calls, map[string]string{})
	if runs != 1 {
		t.Fatalf("the tool ran %d times, want 1", runs)
	}
	if got[0].dup {
		t.Fatal("the first occurrence should not be a duplicate")
	}
	if !got[1].dup || !got[2].dup {
		t.Fatalf("later repeats should be marked dup: %+v", got)
	}
}

// A call already made in an earlier round is answered from the cache.
func TestAlreadyCachedCallIsNotExecuted(t *testing.T) {
	var runs int32
	reg := tools.NewRegistry()
	reg.AddWith(counting{name: "read", runs: &runs}, true, true)
	cached := map[string]string{callKey(Call{Name: "read", Args: argsFor("same")}): "earlier answer"}
	got := executeCalls(context.Background(), reg, allow("read"), nil, "build",
		[]Call{{Name: "read", Args: argsFor("same")}}, cached)
	if runs != 0 {
		t.Fatalf("a cached call was executed %d times", runs)
	}
	if !got[0].dup {
		t.Fatal("expected the call to be marked dup")
	}
}

// Errors have to survive, because a failed tool is exactly what the model
// needs in order to fix its call.
func TestErrorsSurviveParallelExecution(t *testing.T) {
	reg := tools.NewRegistry()
	reg.AddWith(tool{name: "read", run: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "partial output", fmt.Errorf("deliberate failure")
	}}, true, true)
	calls := []Call{
		{Name: "read", Args: argsFor("a")},
		{Name: "read", Args: argsFor("b")},
	}
	got := executeCalls(context.Background(), reg, allow("read"), nil, "build", calls, map[string]string{})
	for i, o := range got {
		if o.err == nil {
			t.Fatalf("outcome %d lost its error", i)
		}
		if !strings.Contains(o.err.Error(), "deliberate") {
			t.Fatalf("outcome %d error = %v", i, o.err)
		}
		if o.res != "partial output" {
			t.Fatalf("outcome %d lost the partial output: %q", i, o.res)
		}
	}
}

// A single call must not pay for a goroutine.
func TestSingleCallRunsInline(t *testing.T) {
	r := &instrumented{name: "read", concurrent: true}
	reg := tools.NewRegistry()
	reg.AddWith(r, true, true)
	got := executeCalls(context.Background(), reg, allow("read"), nil, "build",
		[]Call{{Name: "read", Args: argsFor("only")}}, map[string]string{})
	if len(got) != 1 || !strings.Contains(got[0].res, "only") {
		t.Fatalf("got %+v", got)
	}
	if r.peakConcurrency() != 1 {
		t.Fatalf("peak %d for a single call", r.peakConcurrency())
	}
}

func TestEmptyCallList(t *testing.T) {
	got := executeCalls(context.Background(), tools.NewRegistry(), nil, nil, "build", nil, map[string]string{})
	if len(got) != 0 {
		t.Fatalf("got %d outcomes", len(got))
	}
}

// The cap exists so a model asking for fifty reads does not open fifty file
// handles at once.
func TestParallelismIsCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	r := &instrumented{name: "read", delay: 30 * time.Millisecond, concurrent: true}
	reg := tools.NewRegistry()
	reg.AddWith(r, true, true)
	calls := make([]Call, 40)
	for i := range calls {
		calls[i] = Call{Name: "read", Args: argsFor(fmt.Sprint(i))}
	}
	executeCalls(context.Background(), reg, allow("read"), nil, "build", calls, map[string]string{})
	if p := r.peakConcurrency(); p > maxParallelTools {
		t.Fatalf("peak concurrency %d exceeds the cap of %d", p, maxParallelTools)
	}
	if p := r.peakConcurrency(); p < 2 {
		t.Fatalf("peak concurrency %d; nothing ran in parallel", p)
	}
	if r.startCount() != 40 {
		t.Fatalf("ran %d of 40 calls", r.startCount())
	}
}

// Cancelled work has to return, including work on another goroutine.
func TestCancellationDoesNotHang(t *testing.T) {
	slow := &instrumented{name: "read", delay: 2 * time.Second, concurrent: true}
	reg := tools.NewRegistry()
	reg.AddWith(slow, true, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		executeCalls(ctx, reg, allow("read"), nil, "build",
			[]Call{{Name: "read", Args: argsFor("x")}}, map[string]string{})
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled work did not return")
	}
}

// A mixed message, many rounds, checking that every outcome stays aligned with
// its call position. This is the one that would catch a stray write to a shared
// index - each goroutine writes its own slot, and nothing reads dst until the
// group has finished.
func TestParallelStressKeepsOutcomesAligned(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	for round := 0; round < 50; round++ {
		readers := []*instrumented{
			{name: "r0", delay: 1 * time.Millisecond, concurrent: true},
			{name: "r1", delay: 2 * time.Millisecond, concurrent: true},
			{name: "r2", delay: 3 * time.Millisecond, concurrent: true},
		}
		writer := &instrumented{name: "w", delay: 0, concurrent: false}
		reg := tools.NewRegistry()
		for _, rdr := range readers {
			reg.AddWith(rdr, true, true)
		}
		reg.AddWith(writer, false, false)

		var calls []Call
		var want []string
		for i := 0; i < 9; i++ {
			if i%4 == 3 {
				calls = append(calls, Call{Name: "w", Args: argsFor(fmt.Sprintf("w%d", i))})
				want = append(want, fmt.Sprintf("w%d", i))
			} else {
				n := fmt.Sprintf("r%d", i%3)
				calls = append(calls, Call{Name: n, Args: argsFor(fmt.Sprintf("c%d", i))})
				want = append(want, fmt.Sprintf("c%d", i))
			}
		}
		got := executeCalls(context.Background(), reg, allow("r0", "r1", "r2", "w"), nil, "build",
			calls, map[string]string{})
		if len(got) != len(calls) {
			t.Fatalf("round %d: %d outcomes for %d calls", round, len(got), len(calls))
		}
		for i := range want {
			if !strings.Contains(got[i].res, want[i]) {
				t.Fatalf("round %d: outcome %d = %q, want %q", round, i, got[i].res, want[i])
			}
		}
	}
}

// The whole point, measured: a round of reads is bounded by the slowest read
// rather than by their sum. The delay stands in for disk latency, which is what
// the read tools actually spend their time on.
func BenchmarkExecuteCalls(b *testing.B) {
	const (
		reads = 6
		delay = 20 * time.Millisecond
	)
	for _, concurrent := range []bool{true, false} {
		name := "sequential"
		if concurrent {
			name = "concurrent"
		}
		b.Run(name, func(b *testing.B) {
			reg := tools.NewRegistry()
			reg.AddWith(&instrumented{name: "read", delay: delay, concurrent: concurrent}, concurrent, concurrent)
			calls := make([]Call, reads)
			for i := range calls {
				calls[i] = Call{Name: "read", Args: argsFor(fmt.Sprint(i))}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeCalls(context.Background(), reg, allow("read"), nil, "build", calls, map[string]string{})
			}
		})
	}
}

// --- test doubles ---

// tool is a Tool backed by a function.
type tool struct {
	name string
	run  func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t tool) Name() string        { return t.name }
func (t tool) Description() string { return t.name }
func (t tool) Schema() string      { return `{"type":"object"}` }
func (t tool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return t.run(ctx, args)
}

// counting is a tool that just counts its invocations.
type counting struct {
	name string
	runs *int32
}

func (c counting) Name() string        { return c.name }
func (c counting) Description() string { return "counts" }
func (c counting) Schema() string      { return `{"type":"object"}` }
func (c counting) Run(context.Context, json.RawMessage) (string, error) {
	atomic.AddInt32(c.runs, 1)
	return "ok", nil
}

// sharedWorld is the thing the ordering test reads and writes. A mutex because
// the parallel path really does touch it from several goroutines, and the test
// would be worthless if it were not itself race free.
type sharedWorld struct {
	mu    sync.Mutex
	value string
}

func (w *sharedWorld) get() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.value
}

func (w *sharedWorld) set(v string) {
	w.mu.Lock()
	w.value = v
	w.mu.Unlock()
}
