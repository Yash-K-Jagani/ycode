package tui

import (
	"strings"
	"sync"
	"time"
)

// Delta coalescing.
//
// A provider streams a stream in whatever chunks its transport delivers - for a
// local Ollama model on a fast machine that can be dozens of writes a second,
// each one a bubbletea message, each one a full transcript re-render. The
// rendering is now incremental (see transcript.go), but the *number* of updates
// is still set by how chatty the transport is rather than by how much the screen
// can usefully change in a frame.
//
// Twenty-five writes a second is well past what a terminal can show. Batching
// them into at most one update per interval costs nothing perceptible - a person
// cannot read faster than 40ms - and it caps the work per second regardless of
// how fast the model is going.
//
// The trailing text is not at risk. A turn always ends by re-rendering the
// completed message in full, so anything still buffered when the stream ends is
// superseded rather than lost. That is why this needs no background goroutine:
// there is no timer to stop, no goroutine to leak, and no race between a late
// flush and the turn finishing. Flush is explicit and called by the turn.

// coalesceInterval is how often the transcript may be re-rendered while a
// response streams. A terminal frame is far slower than this.
const coalesceInterval = 40 * time.Millisecond

// coalescingWriter batches streamed chunks so the UI is updated at most once per
// interval, while never withholding more than that interval's worth of text.
//
// It satisfies io.Writer, so it drops in wherever progWriter did.
type coalescingWriter struct {
	mu      sync.Mutex
	pending strings.Builder
	send    func(string)
	// interval is the minimum time between two forwarded updates.
	interval time.Duration
	// last is when a chunk was last handed on, used to decide whether the next
	// one is due. Zero means "nothing sent yet", so the first write goes
	// straight through and the user sees the first token immediately rather than
	// after a delay.
	last time.Time
	// now is the clock, replaceable so the tests do not have to sleep.
	now func() time.Time
	// onWrite, if set, is called with the length of every chunk received,
	// whether or not it was forwarded. Token accounting needs the total, and
	// counting only what reached the UI would under-report a turn whose tail was
	// still buffered when the stream ended.
	onWrite func(n int)
	// chunks counts writes received, updates counts forwards, so the reduction
	// is observable rather than assumed.
	chunks  int
	updates int
}

func newCoalescingWriter(send func(string), interval time.Duration) *coalescingWriter {
	return &coalescingWriter{
		send:     send,
		now:      time.Now,
		interval: interval,
	}
}

// Write buffers p and forwards it if the interval has elapsed.
//
// The returned count is always len(p): from the caller's point of view the text
// has been accepted, and reporting a short write would make the provider treat
// the connection as failed.
func (c *coalescingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.chunks++
	c.pending.Write(p)
	due := c.now().Sub(c.last) >= c.interval
	if due {
		c.flushLocked()
	}
	c.mu.Unlock()
	if c.onWrite != nil {
		c.onWrite(len(p))
	}
	return len(p), nil
}

// Flush hands on everything buffered, so the UI is up to date immediately.
//
// Called when a turn ends, ahead of the completion message. Without it the last
// few dozen characters of a response would sit in the buffer until something
// else arrived to trigger a write.
func (c *coalescingWriter) Flush() {
	c.mu.Lock()
	c.flushLocked()
	c.mu.Unlock()
}

// flushLocked does the work of a flush with the lock already held.
func (c *coalescingWriter) flushLocked() {
	// Record the time even when there was nothing to send, so a run of empty
	// writes cannot spin.
	c.last = c.now()
	if c.pending.Len() == 0 {
		return
	}
	text := c.pending.String()
	c.pending.Reset()
	c.updates++
	if c.send != nil {
		c.send(text)
	}
}

// stats reports how many writes arrived and how many were forwarded, for tests
// and for /debug.
func (c *coalescingWriter) stats() (chunks, updates int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chunks, c.updates
}
