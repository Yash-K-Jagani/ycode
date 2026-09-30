package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the coalescing tests run instantly and deterministically.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(0, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// The whole point: many writes in, far fewer updates out, and the concatenated
// result is identical. A coalescer that dropped or reordered text would corrupt
// the transcript in a way that is very hard to spot by eye.
func TestCoalescingWriterReducesUpdates(t *testing.T) {
	clock := newFakeClock()
	var got []string
	cw := newCoalescingWriter(func(s string) { got = append(got, s) }, 40*time.Millisecond)
	cw.now = clock.now

	var sent strings.Builder
	// 200 chunks, one every millisecond: 200ms of stream, which at 40ms should
	// be about five updates.
	for i := 0; i < 200; i++ {
		if _, err := cw.Write([]byte("x")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		clock.advance(time.Millisecond)
	}
	cw.Flush()

	chunks, updates := cw.stats()
	if chunks != 200 {
		t.Fatalf("chunks = %d, want 200", chunks)
	}
	if updates >= chunks {
		t.Fatalf("no reduction: %d updates for %d chunks", updates, chunks)
	}
	// 200ms of stream at 40ms is about 5-6 updates. Allow slack for the leading
	// edge, but not for a pass-through.
	if updates > 12 {
		t.Fatalf("expected coalescing, got %d updates for %d chunks", updates, chunks)
	}
	if sent.String() != "" {
		t.Fatal("unused")
	}
	if strings.Join(got, "") != strings.Repeat("x", 200) {
		t.Fatalf("text was lost or reordered: got %d chars in %d pieces",
			len(strings.Join(got, "")), len(got))
	}
}

// The first token must appear immediately. Buffering it would make the start of
// every response feel late, and "last" starts at the zero time precisely so the
// first write is always due.
func TestCoalescingWriterSendsFirstWriteImmediately(t *testing.T) {
	clock := newFakeClock()
	var got []string
	cw := newCoalescingWriter(func(s string) { got = append(got, s) }, 40*time.Millisecond)
	cw.now = clock.now

	if _, err := cw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("first write was buffered: %v", got)
	}
}

// Nothing may be withheld beyond the interval. A write that arrives after the
// interval has passed goes straight out, so the worst-case delay for any text is
// one interval, not unbounded.
func TestCoalescingWriterNeverWithholdsPastTheInterval(t *testing.T) {
	clock := newFakeClock()
	var got []string
	cw := newCoalescingWriter(func(s string) { got = append(got, s) }, 40*time.Millisecond)
	cw.now = clock.now

	// A coalescing writer never fails, so the count is noise here. Checked
	// anyway so a future change that made Write return an error would not pass
	// this test by accident.
	mustWrite := func(s string) {
		t.Helper()
		if n, err := cw.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	mustWrite("a")
	mustWrite("b") // buffered
	clock.advance(39 * time.Millisecond)
	mustWrite("c") // still within the interval
	if len(got) != 1 {
		t.Fatalf("forwarded before the interval elapsed: %v", got)
	}
	clock.advance(2 * time.Millisecond)
	mustWrite("d") // now due
	if len(got) != 2 {
		t.Fatalf("expected a second update once the interval passed, got %v", got)
	}
	if got[1] != "bcd" {
		t.Fatalf("buffered text = %q, want %q", got[1], "bcd")
	}
}

// checkWrites asserts the writer consumed everything, which a coalescing writer
// always does. Used instead of bare cw.Write calls so errcheck is satisfied and
// a future change that made Write fail would fail here rather than silently
// making the timing assertions below meaningless.
func checkWrites(t *testing.T, cw io.Writer, chunks ...string) {
	t.Helper()
	for _, c := range chunks {
		if n, err := cw.Write([]byte(c)); err != nil || n != len(c) {
			t.Fatalf("Write(%q) = %d, %v", c, n, err)
		}
	}
}

// A turn ends with whatever is left in the buffer, and the completed message is
// re-rendered in full afterwards, so the last words must not sit in limbo.
func TestCoalescingWriterFlushEmitsTail(t *testing.T) {
	clock := newFakeClock()
	var got []string
	cw := newCoalescingWriter(func(s string) { got = append(got, s) }, 40*time.Millisecond)
	cw.now = clock.now

	checkWrites(t, cw, "start", " middle", " end")
	cw.Flush()
	if strings.Join(got, "") != "start middle end" {
		t.Fatalf("got %q", strings.Join(got, ""))
	}
	// A second flush with nothing pending must not send an empty update, which
	// would re-render the screen for no reason.
	before := len(got)
	cw.Flush()
	if len(got) != before {
		t.Fatalf("an empty flush emitted an update: %v", got)
	}
}

// Token accounting counts every byte the model produced, not just the bytes that
// happened to be forwarded. A tail left in the buffer when the stream ended is
// still output the provider produced and the user paid for.
func TestCoalescingWriterCountsAllBytes(t *testing.T) {
	clock := newFakeClock()
	total := 0
	cw := newCoalescingWriter(func(string) {}, 40*time.Millisecond)
	cw.now = clock.now
	cw.onWrite = func(n int) { total += n }

	for i := 0; i < 50; i++ {
		checkWrites(t, cw, "abcd")
	}
	if total != 200 {
		t.Fatalf("counted %d bytes, want 200", total)
	}
	// And before a flush, the count must already be right: accounting is not
	// allowed to depend on whether the UI has caught up.
	if total != 200 {
		t.Fatalf("count depended on flushing: %d", total)
	}
}

// A short write would make the provider think the connection failed, so the
// length is always the full length however much is buffered.
func TestCoalescingWriterReportsFullLength(t *testing.T) {
	clock := newFakeClock()
	cw := newCoalescingWriter(func(string) {}, time.Hour)
	cw.now = clock.now
	checkWrites(t, cw, "first")
	n, err := cw.Write([]byte("second"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != len("second") {
		t.Fatalf("short write: %d of %d", n, len("second"))
	}
}

// The turn goroutine and the UI are different goroutines, so this is the shape
// that matters. Without a data race detector available on every platform, the
// lock is exercised here directly and the counts are checked for consistency.
func TestCoalescingWriterConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	const writers, perWriter = 8, 200
	var mu sync.Mutex
	var got strings.Builder
	cw := newCoalescingWriter(func(s string) {
		mu.Lock()
		got.WriteString(s)
		mu.Unlock()
	}, time.Nanosecond)

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				checkWrites(t, cw, "y")
			}
		}()
	}
	wg.Wait()
	cw.Flush()

	mu.Lock()
	n := got.Len()
	mu.Unlock()
	if n != writers*perWriter {
		t.Fatalf("forwarded %d bytes, want %d", n, writers*perWriter)
	}
	chunks, updates := cw.stats()
	if chunks != writers*perWriter {
		t.Fatalf("chunks = %d, want %d", chunks, writers*perWriter)
	}
	if updates > chunks {
		t.Fatalf("more updates than chunks: %d > %d", updates, chunks)
	}
}

// A nil send must not panic: the turn passes a closure that checks prog, and a
// test may pass nothing at all.
func TestCoalescingWriterNilSendIsSafe(t *testing.T) {
	clock := newFakeClock()
	cw := newCoalescingWriter(nil, time.Hour)
	cw.now = clock.now
	checkWrites(t, cw, string([]byte("text")))
	cw.Flush()
}

func TestCoalescingWriterEmptyWrites(t *testing.T) {
	clock := newFakeClock()
	var got []string
	// A zero interval makes every write due, so this tests the empty-write path
	// rather than the coalescing.
	cw := newCoalescingWriter(func(s string) { got = append(got, s) }, 0)
	cw.now = clock.now
	for i := 0; i < 10; i++ {
		checkWrites(t, cw, "")
	}
	if len(got) != 0 {
		t.Fatalf("empty writes produced updates: %v", got)
	}
	checkWrites(t, cw, string([]byte("real")))
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("got %v", got)
	}
}

func BenchmarkCoalescingWriter(b *testing.B) {
	bench := []struct {
		name     string
		interval time.Duration
	}{
		{"passthrough", 0},
		{"coalesced-40ms", coalesceInterval},
	}
	for _, tc := range bench {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			cw := newCoalescingWriter(func(string) {}, tc.interval)
			buf := []byte("token")
			for i := 0; i < b.N; i++ {
				// Discarded rather than asserted: this measures the write path,
				// and a coalescing writer never errors. The functional tests cover
				// that separately, where a failure has somewhere to be reported.
				_, _ = cw.Write(buf)
			}
			cw.Flush()
		})
	}
}
