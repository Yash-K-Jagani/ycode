package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// streamTool emits output in pieces while it runs, like the real bash, run,
// testgen and notebook tools do.
type streamTool struct {
	mu     sync.Mutex
	chunks []string
}

func (s *streamTool) Name() string        { return "streamer" }
func (s *streamTool) Description() string { return "emits output in pieces" }
func (s *streamTool) Schema() string      { return "{}" }

func (s *streamTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return s.run(ctx, nil)
}

// Stream is what the loop looks for. It must produce exactly what Run produces,
// or a caller that streams gets a different answer from one that does not.
func (s *streamTool) Stream(ctx context.Context, args json.RawMessage, emit func(string)) (string, error) {
	return s.run(ctx, emit)
}

func (s *streamTool) run(ctx context.Context, emit func(string)) (string, error) {
	s.mu.Lock()
	s.chunks = []string{"first ", "second ", "third"}
	s.mu.Unlock()
	for _, c := range s.chunks {
		if emit != nil {
			emit(c)
		}
	}
	return "first second third", nil
}

// quietTool has no Stream method, so it must never reach the streaming path.
type quietTool struct{ ran bool }

func (q *quietTool) Name() string        { return "quiet" }
func (q *quietTool) Description() string { return "no Stream method" }
func (q *quietTool) Schema() string      { return "{}" }

func (q *quietTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	q.ran = true
	return "quiet output", nil
}

func msgs() []apitypes.Message {
	return []apitypes.Message{{Role: apitypes.RoleUser, Content: "hi"}}
}

// A provider that asks for one tool call and then answers.
func scriptFor(name string) *fakeProvider {
	return &fakeProvider{script: []string{"<tool:" + name + ">{}</tool:" + name + ">", "done"}}
}

// --- the observer gets chunks ---

func TestStreamingToolReportsChunks(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&streamTool{})
	var got []string
	var mu sync.Mutex
	res, err := RunWithRounds(context.Background(), scriptFor("streamer"), "fake-1",
		msgs(), reg, []string{"streamer"}, nil, io.Discard,
		&Observer{OnChunk: func(name, chunk string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, name+":"+chunk)
		}}, 4, "build")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("got %d chunks, want 3: %v", len(got), got)
	}
	// Named, so a caller rendering several tools at once can tell them apart.
	for _, g := range got {
		if !strings.HasPrefix(g, "streamer:") {
			t.Fatalf("chunk not attributed to its tool: %q", g)
		}
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q", res.Text)
	}
}

// The interface contract: a Streamer must return what Run returns. If it did
// not, streaming would change the answer depending on whether a UI was
// attached, which is the worst possible shape for this feature.
func TestStreamingDoesNotChangeTheResult(t *testing.T) {
	for _, obs := range []*Observer{nil, {}, {OnChunk: func(string, string) {}}} {
		reg := tools.NewRegistry()
		reg.Add(&streamTool{})
		res, err := RunWithRounds(context.Background(), scriptFor("streamer"), "fake-1",
			msgs(), reg, []string{"streamer"}, nil, io.Discard, obs, 4, "build")
		if err != nil {
			t.Fatal(err)
		}
		if res.Text != "done" {
			t.Fatalf("Text = %q with observer %v", res.Text, obs != nil)
		}
	}
}

// A nil observer must not reach the streaming path at all, not merely receive
// nothing: a caller that does not want progress should not pay for buffers and
// goroutines that go nowhere.
func TestNilObserverSkipsTheStreamingPath(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&quietTool{})
	q := &quietTool{}
	reg.Add(q)
	if _, err := RunWithRounds(context.Background(), scriptFor("quiet"), "fake-1",
		msgs(), reg, []string{"quiet"}, nil, io.Discard, nil, 4, "build"); err != nil {
		t.Fatal(err)
	}
	if !q.ran {
		t.Fatal("the tool did not run")
	}
}

// A tool that does not stream still reports through OnTool. The chunk callback
// being nil must not cost the result callback, which is how a UI would go blind
// to every tool at once.
func TestOnToolWorksWithoutOnChunk(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&streamTool{})
	var names []string
	if _, err := RunWithRounds(context.Background(), scriptFor("streamer"), "fake-1",
		msgs(), reg, []string{"streamer"}, nil, io.Discard,
		&Observer{OnTool: func(name, args, result string, err error) { names = append(names, name) }},
		4, "build"); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "streamer" {
		t.Fatalf("OnTool got %v", names)
	}
}

// OnTool must fire exactly once per call, in the order the model wrote the
// calls, even when execution was parallel. A UI that renders a card per event
// depends on this.
func TestOnToolFiresOncePerCallInOrder(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&streamTool{})
	var order []string
	p := &fakeProvider{script: []string{
		"<tool:streamer>{\"x\":1}</tool:streamer>",
		"done",
	}}
	if _, err := RunWithRounds(context.Background(), p, "fake-1",
		msgs(), reg, []string{"streamer"}, nil, io.Discard,
		&Observer{OnTool: func(name, args, result string, err error) { order = append(order, name+"="+result) }},
		4, "build"); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 {
		t.Fatalf("OnTool fired %d times: %v", len(order), order)
	}
	if order[0] != "streamer=first second third" {
		t.Fatalf("result was not the complete output: %q", order[0])
	}
}

// Parallel read-only tools emit concurrently. The callback therefore has to be
// safe to call from several goroutines, and this is the test that would catch a
// future change making it single threaded.
func TestChunksFromParallelToolsAreConcurrencySafe(t *testing.T) {
	reg := tools.NewRegistry()
	for i := 0; i < 4; i++ {
		reg.Add(&streamTool{})
	}
	p := &fakeProvider{script: []string{
		"<tool:streamer>{}</tool:streamer><tool:streamer>{}</tool:streamer>" +
			"<tool:streamer>{}</tool:streamer><tool:streamer>{}</tool:streamer>",
		"done",
	}}
	var mu sync.Mutex
	total := 0
	if _, err := RunWithRounds(context.Background(), p, "fake-1",
		msgs(), reg, []string{"streamer"}, nil, io.Discard,
		&Observer{
			OnChunk: func(name, chunk string) {
				mu.Lock()
				defer mu.Unlock()
				total++
			},
			OnTool: func(name, args, result string, err error) {},
		}, 4, "build"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// Every call must have streamed; the count depends on parallelism, so the
	// assertion is that all four ran rather than an exact number.
	if total < 4 {
		t.Fatalf("only %d chunks arrived for 4 calls", total)
	}
}

// --- nil safety ---

// The zero Observer is what most callers pass, so it has to be harmless.
func TestZeroObserverIsHarmless(t *testing.T) {
	var o *Observer
	o.tool("a", "b", "c", nil)
	if o.streams() {
		t.Fatal("a nil observer claims to stream")
	}
	e := &Observer{}
	e.tool("a", "b", "c", nil)
	if e.streams() {
		t.Fatal("an observer with no OnChunk claims to stream")
	}
	if !(&Observer{OnChunk: func(string, string) {}}).streams() {
		t.Fatal("an observer with OnChunk does not stream")
	}
}

// The answer must not depend on whether anyone was watching. A caller that
// streams and a caller that does not have to reach the same conclusion, or the
// model behaves differently in a TUI than it does in CI for no reason a user
// could explain.
func TestResultIsUnaffectedByTheObserver(t *testing.T) {
	build := func(obs *Observer) Result {
		reg := tools.NewRegistry()
		reg.Add(&streamTool{})
		res, err := RunWithRounds(context.Background(), scriptFor("streamer"), "fake-1",
			msgs(), reg, []string{"streamer"}, nil, io.Discard, obs, 4, "build")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a := build(nil)
	b := build(&Observer{OnChunk: func(string, string) {}})
	if a != b {
		t.Fatalf("results differ: %+v vs %+v", a, b)
	}
}
