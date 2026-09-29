package httpx

import (
	"context"
	"io"
	"sync"
	"time"
)

// The stream idle watchdog.
//
// A streaming response is open for as long as the model is generating, and it
// is idle between chunks the whole time - a 3B model on modest hardware pauses
// for hundreds of milliseconds routinely. So the length of the stream says
// nothing about whether it is healthy; the gap between chunks does.
//
// Without a watchdog the only options were a blanket timeout, which killed
// legitimate long generations mid-sentence, and no timeout, which left a dead
// connection looking exactly like a slow one. Cancelling the request context
// is what makes a stalled stream detectable: net/http interrupts a blocked read
// on a cancelled context and closes the connection, so a stuck provider becomes
// an error the caller sees immediately rather than a turn that hangs.

// StreamWatch is a handle on an installed idle watchdog.
type StreamWatch struct {
	r      io.Reader
	mu     sync.Mutex
	last   time.Time
	idle   time.Duration
	cancel context.CancelFunc
	stop   chan struct{}
	fired  bool
	closed sync.Once
}

// WatchIdle returns a reader that cancels cancel() if no bytes are read for
// idle, plus a handle for stopping and querying the watchdog.
//
// A non-positive idle installs no watchdog but still returns a usable handle,
// so callers never have to nil-check one. The handle is always safe to Close
// and always safe to ask whether it fired; it simply never fires.
func WatchIdle(cancel context.CancelFunc, r io.Reader, idle time.Duration) (io.Reader, *StreamWatch) {
	w := &StreamWatch{
		r:      r,
		last:   time.Now(),
		idle:   idle,
		cancel: cancel,
		stop:   make(chan struct{}),
	}
	if idle <= 0 {
		return r, w
	}
	// Check at a fraction of the bound so a stall is noticed promptly after it
	// elapses rather than up to a full bound late.
	tick := idle / 4
	if tick < 50*time.Millisecond {
		tick = 50 * time.Millisecond
	}
	go w.watch(tick)
	return w, w
}

func (w *StreamWatch) watch(tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.mu.Lock()
			idle := time.Since(w.last)
			if idle > w.idle {
				w.fired = true
			}
			w.mu.Unlock()
			if idle > w.idle {
				// Cancelling the request context is what unblocks a read
				// already in progress; closing the body is the belt to that
				// braces, in case nothing is reading yet.
				w.cancel()
				return
			}
		}
	}
}

func (w *StreamWatch) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	// Updated on every read, including a zero-byte one that reports an error,
	// so the watchdog is not racing the final read of a stream that is
	// finishing normally.
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
	return n, err
}

// Close stops the watchdog. It is safe to call more than once.
func (w *StreamWatch) Close() {
	w.closed.Do(func() { close(w.stop) })
}

// Fired reports whether the watchdog gave up, which is what distinguishes a
// dead provider from a provider that was cut off by a deadline, a reset
// connection, or a cancelled turn. The error a stalled read produces is the
// transport's, not ours, so this is the only reliable way to tell them apart.
func (w *StreamWatch) Fired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

// WithStreamIdle installs an idle watchdog on a streaming request and returns
// the guarded body together with its handle.
//
// ctx must be a cancellable child of the caller's context: cancelling it is how
// a stalled stream is interrupted. The returned function cancels that child,
// releases the body and stops the watchdog, and is the only cleanup the caller
// needs.
func WithStreamIdle(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, idle time.Duration) (io.Reader, func(), *StreamWatch) {
	r, w := WatchIdle(cancel, body, idle)
	return r, func() {
		w.Close()
		_ = body.Close()
		cancel()
	}, w
}
