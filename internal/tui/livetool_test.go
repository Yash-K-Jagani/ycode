package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// gateSpy is a chunkGate wired to a collector instead of a message loop, so
// batching can be asserted on directly.
type gateSpy struct {
	g      *chunkGate
	mu     sync.Mutex
	msgs   []toolChunkMsg
	starts time.Time
}

func newChunkGateForTest() *gateSpy {
	s := &gateSpy{starts: time.Now()}
	s.g = &chunkGate{
		post:  func(m toolChunkMsg) { s.collect(m) },
		last:  time.Now(),
		names: map[string]bool{},
	}
	return s
}

func (s *gateSpy) collect(m toolChunkMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
}

func (s *gateSpy) posted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

func (s *gateSpy) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.msgs))
	for _, m := range s.msgs {
		out = append(out, m.chunk)
	}
	return out
}

func (s *gateSpy) held() int {
	s.g.mu.Lock()
	defer s.g.mu.Unlock()
	return len(s.g.held)
}

// --- the live block ---

func TestChunkShowsWhileTheToolRuns(t *testing.T) {
	m := &Model{}
	m.Update(toolChunkMsg{tool: "bash", chunk: "compiling foo\n"})
	if m.liveTool != "bash" {
		t.Fatalf("liveTool = %q", m.liveTool)
	}
	prev := m.livePreview()
	if !strings.Contains(prev, "bash") || !strings.Contains(prev, "compiling foo") {
		t.Fatalf("preview does not show the output: %q", prev)
	}
	// More output appends rather than replaces what is already there, or a
	// chatty tool would only ever show its final bytes.
	m.Update(toolChunkMsg{tool: "bash", chunk: "linking bar\n"})
	if got := m.livePreview(); !strings.Contains(got, "compiling foo") || !strings.Contains(got, "linking bar") {
		t.Fatalf("second chunk replaced the first: %q", got)
	}
}

// The live block is provisional. It lives outside the transcript and is dropped
// when the call finishes, because the complete output arrives then as a
// permanent card - keeping both would show a command's output twice.
func TestLiveBlockIsNotInTheTranscript(t *testing.T) {
	m := &Model{}
	m.Update(toolChunkMsg{tool: "bash", chunk: "partial output\n"})
	if strings.Contains(strings.Join(m.msgs, "\n"), "partial output") {
		t.Fatal("live output was committed to the transcript")
	}
	if m.livePreview() == "" {
		t.Fatal("nothing is shown live")
	}
}

func TestCompletionDropsTheLiveBlock(t *testing.T) {
	m := &Model{}
	m.Update(toolChunkMsg{tool: "bash", chunk: "compiling foo\n"})
	m.Update(toolMsg{name: "bash", status: "ok (2 bytes)", out: "compiling foo\ndone"})
	if m.liveTool != "" || len(m.liveBuf) != 0 {
		t.Fatal("the provisional block survived completion")
	}
	if m.livePreview() != "" {
		t.Fatalf("a stale preview is still rendered: %q", m.livePreview())
	}
	// The complete output goes in permanently, exactly once.
	body := strings.Join(m.msgs, "\n")
	if strings.Count(body, "compiling foo") != 1 {
		t.Fatalf("output shown %d times: %q", strings.Count(body, "compiling foo"), body)
	}
	if !strings.Contains(body, "done") {
		t.Fatalf("the final output is missing: %q", body)
	}
}

// A tool that produces no output gets a plain status line, not an empty card.
func TestCompletionWithoutOutputIsAStatusLine(t *testing.T) {
	m := &Model{}
	m.Update(toolMsg{name: "bash", status: "ok (0 bytes)"})
	body := strings.Join(m.msgs, "\n")
	if !strings.Contains(body, "bash") || !strings.Contains(body, "ok (0 bytes)") {
		t.Fatalf("status not shown: %q", body)
	}
}

// Starting a different tool must not interleave two commands' output into one
// block. This happens when a turn is abandoned mid-call and the previous tool's
// completion message never arrives.
func TestANewToolResetsTheBlock(t *testing.T) {
	m := &Model{}
	m.Update(toolChunkMsg{tool: "bash", chunk: "first command\n"})
	m.Update(toolChunkMsg{tool: "testgen", chunk: "second command\n"})
	got := m.livePreview()
	if !strings.Contains(got, "testgen") {
		t.Fatalf("the live header names the wrong tool: %q", got)
	}
	if strings.Contains(got, "first command") {
		t.Fatalf("output from two tools was interleaved: %q", got)
	}
}

func TestCancelledTurnIgnoresChunks(t *testing.T) {
	m := &Model{cancelled: true}
	m.Update(toolChunkMsg{tool: "bash", chunk: "output"})
	if m.liveTool != "" || m.livePreview() != "" {
		t.Fatal("a cancelled turn still rendered tool output")
	}
}

// --- bounds ---

// Every chunk re-renders the block. An unbounded buffer turns a chatty build
// into a frozen terminal, which is the failure live output was added to avoid.
func TestLiveBufferIsBounded(t *testing.T) {
	m := &Model{}
	big := strings.Repeat("x", maxLiveToolBytes)
	for i := 0; i < 10; i++ {
		m.Update(toolChunkMsg{tool: "bash", chunk: big})
	}
	if len(m.liveBuf) > maxLiveToolBytes {
		t.Fatalf("live buffer grew to %d bytes, past the %d cap", len(m.liveBuf), maxLiveToolBytes)
	}
	if len(m.livePreview()) == 0 {
		t.Fatal("nothing rendered after a flood")
	}
}

func TestOnlyTheLastLinesAreDrawn(t *testing.T) {
	m := &Model{}
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteString("\n")
	}
	m.Update(toolChunkMsg{tool: "testgen", chunk: b.String()})
	got := m.livePreview()
	if n := strings.Count(got, "\n"); n > maxLiveToolLines+2 {
		t.Fatalf("rendered %d lines, expected at most about %d", n, maxLiveToolLines+2)
	}
}

// The end of a build log is the answer: the failing test, the error, the
// summary. The beginning is progress bars nobody reads.
func TestTailKeepsTheEnd(t *testing.T) {
	got := tailLines("first\nsecond\nthird", 2)
	if strings.Contains(got, "first") {
		t.Fatalf("the head was kept: %q", got)
	}
	if !strings.Contains(got, "second") || !strings.Contains(got, "third") {
		t.Fatalf("the tail was not kept: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Fatalf("truncation is not marked: %q", got)
	}
}

func TestTailLeavesShortOutputAlone(t *testing.T) {
	if got := tailLines("only\ntwo", 5); got != "only\ntwo" {
		t.Fatalf("short output was altered: %q", got)
	}
}

func TestCapOutputBoundsThePermanentCard(t *testing.T) {
	got := capOutput(strings.Repeat("y", maxToolOutBytes*3))
	if len(got) > maxToolOutBytes+8 {
		t.Fatalf("card is %d bytes, past the cap", len(got))
	}
	if !strings.HasPrefix(got, "…") {
		t.Fatal("truncation is not marked")
	}
	if len(capOutput("small")) != 5 {
		t.Fatal("short output was altered")
	}
}

// Only streaming tools put their output in the transcript. A read tool's output
// is a file, and putting that in the transcript uninvited is a different change
// with a different justification.
func TestOnlyStreamingToolsPutOutputInTheCard(t *testing.T) {
	if got := streamedOutput(false, "contents of a file"); got != "" {
		t.Fatalf("a non-streaming tool's output was kept: %q", got)
	}
	if got := streamedOutput(true, "command output"); got != "command output" {
		t.Fatalf("a streaming tool's output was dropped: %q", got)
	}
}

// --- the gate ---

// A build that prints ten thousand lines must not post ten thousand messages.
// The gate exists for that, and this is the test that would catch it regressing
// into a per-chunk Send.
func TestGateCollapsesABurst(t *testing.T) {
	s := newChunkGateForTest()
	for i := 0; i < 5000; i++ {
		s.g.emit("bash", "line\n")
	}
	s.g.flush()
	n := s.posted()
	if n == 0 {
		t.Fatal("nothing was posted")
	}
	if n > 20 {
		t.Fatalf("5000 chunks became %d messages; the gate is not collapsing them", n)
	}
}

// Batching must not lose output. A dropped chunk leaves a gap in the middle of
// a build log, and a gap is worse than a slightly late line.
func TestGateLosesNothing(t *testing.T) {
	spy := newChunkGateForTest()
	var want strings.Builder
	for i := 0; i < 500; i++ {
		line := "chunk" + strings.Repeat("z", 5) + "\n"
		want.WriteString(line)
		spy.g.emit("bash", line)
	}
	spy.g.flush()
	got := strings.Join(spy.all(), "")
	if got != want.String() {
		t.Fatalf("the gate altered the output.\n got %d bytes, want %d", len(got), want.Len())
	}
}

// A tool that floods between two interval boundaries would otherwise be held
// whole in memory while waiting for the next push.
func TestGateBoundsWhatItHolds(t *testing.T) {
	s := newChunkGateForTest()
	// The gate is fresh, so the first emit posts; force the hold by emitting
	// many times inside one interval.
	for i := 0; i < 200; i++ {
		s.g.emit("bash", strings.Repeat("q", 1024))
	}
	if s.held() > maxLiveToolBytes {
		t.Fatalf("gate is holding %d bytes, past the cap", s.held())
	}
}

// A nil program means no message loop. The gate has to be a working no-op
// rather than a nil dereference, because every caller that has no program still
// has to construct one.
func TestGateWithoutAProgramIsHarmless(t *testing.T) {
	g := newChunkGate(nil)
	if g != nil {
		t.Fatal("a gate was built for a nil program")
	}
	g.emit("bash", "output")
	g.flush()
}

// --- naming ---

// Parallel tools interleave, so the live header has to name all of them rather
// than blaming one tool for another's output.
func TestGateNamesEveryContributor(t *testing.T) {
	if got := gateNames(map[string]bool{"bash": true}); got != "bash" {
		t.Fatalf("single name = %q", got)
	}
	got := gateNames(map[string]bool{"testgen": true, "bash": true})
	if !strings.Contains(got, "bash") || !strings.Contains(got, "testgen") {
		t.Fatalf("not all contributors named: %q", got)
	}
	// Sorted, so the header does not reshuffle between renders and flicker.
	if got != "bash+testgen" {
		t.Fatalf("names are not in a stable order: %q", got)
	}
}

func TestChunkIntervalIsSane(t *testing.T) {
	// Too long and the live view lags behind reality; too short and it is just
	// the per-chunk path with extra steps.
	if chunkInterval < 10*time.Millisecond || chunkInterval > 500*time.Millisecond {
		t.Fatalf("chunkInterval = %s", chunkInterval)
	}
}

func TestClearLiveToolEmptiesEverything(t *testing.T) {
	m := &Model{liveTool: "bash", liveBuf: []byte("output")}
	m.clearLiveTool()
	if m.liveTool != "" || len(m.liveBuf) != 0 || m.livePreview() != "" {
		t.Fatal("clearLiveTool left something behind")
	}
}
