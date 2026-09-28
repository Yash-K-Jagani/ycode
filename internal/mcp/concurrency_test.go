package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Client.dead used to be a plain bool guarded by mu, but not all its writers
// can hold that lock: readResponse runs on its own goroutine, and Close is
// called without it. A call after a timeout read the field while the previous
// reader goroutine was still writing it, which the race detector caught on
// Linux. It is an atomic.Bool now.
//
// These tests exist so that reverting it is caught again. The race detector is
// the assertion here - the code is correct either way, it just needs proving -
// so they exercise the paths that actually crossed goroutines: a call racing
// its own timeout, and a call racing Close.

func TestConcurrentCallsAndClose(t *testing.T) {
	c := startSilentServer(t)
	defer c.Close()

	// Several callers hammering the client while it is being closed. Most
	// calls time out or fail; none may corrupt memory or panic.
	var wg sync.WaitGroup
	stop := time.Now().Add(400 * time.Millisecond)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				_, _ = c.ListTools(ctx)
				cancel()
			}
		}()
	}
	// Close partway through, without the lock, as the shutdown path does.
	go func() {
		time.Sleep(100 * time.Millisecond)
		c.Close()
	}()
	wg.Wait()
}

func TestCallRacingItsOwnTimeout(t *testing.T) {
	// A call that times out kills the server from the caller's goroutine
	// while the reader goroutine is still in Scan, then waits for it. The
	// next call reads the dead flag both of them touch.
	c := startSilentServer(t)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			if _, err := c.ListTools(ctx); err == nil {
				t.Error("a call to a silent server should time out")
			}
		}()
	}
	wg.Wait()

	// Every call after the server is gone must fail fast and say so, rather
	// than blocking on a pipe nobody will write to.
	start := time.Now()
	_, err := c.ListTools(context.Background())
	if err == nil {
		t.Fatal("a call after a timeout should fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("a call after a timeout blocked instead of failing fast")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error should say the server is gone, got %v", err)
	}
}

// Close is called from Shutdown on the way out and can land while a turn is
// mid-request, so it must be safe to call more than once and concurrently.
func TestCloseIsSafeWhileBusy(t *testing.T) {
	c := startSilentServer(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, _ = c.ListTools(ctx)
	}()

	time.Sleep(10 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()
	<-done

	// And the client stays poisoned rather than resurrecting.
	if _, err := c.ListTools(context.Background()); err == nil {
		t.Fatal("the client came back after Close")
	}
}
