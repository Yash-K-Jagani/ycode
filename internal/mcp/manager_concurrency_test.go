package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Starting an MCP server is a subprocess launch plus a handshake, so ensure
// does it outside the manager lock. That is what made it racy: two callers
// could both see no client, both launch, and the second assignment would
// overwrite the first - leaving a live MCP child process that nothing held a
// reference to and nothing would ever close.
//
// These tests replace the startClient seam, so no subprocess is spawned, and
// the clients are real Clients over pipes - Initialize, ListTools and Close all
// behave as they do against a real server.

// newPipeClient builds a Client talking to fakeServer over in-process pipes,
// and reports when it is closed.
func newPipeClient(t *testing.T, closed *atomic.Int32) *Client {
	t.Helper()
	c2sR, c2sW, _ := os.Pipe() // client writes, server reads
	s2cR, s2cW, _ := os.Pipe() // server writes, client reads
	t.Cleanup(func() {
		_ = c2sR.Close()
		_ = c2sW.Close()
		_ = s2cR.Close()
		_ = s2cW.Close()
	})
	go fakeServer(t, c2sR, s2cW)

	c := &Client{stdin: *json.NewEncoder(c2sW), closeHook: func() { closed.Add(1) }}
	c.scan = bufio.NewScanner(s2cR)
	c.scan.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	return c
}

// starter counts process launches and can hold one open, so a test can
// interleave Remove or Close with a start that is in flight.
type starter struct {
	t        *testing.T
	launches atomic.Int32
	closed   atomic.Int32
	block    chan struct{}
	// live is the number of clients that have been started and not closed.
	live atomic.Int32
}

func (s *starter) install(t *testing.T) {
	t.Helper()
	t.Helper()
	s.t = t
	prev := startClient
	startClient = s.start
	t.Cleanup(func() { startClient = prev })
}

func (s *starter) start(command string, args []string, env map[string]string) (*Client, error) {
	s.launches.Add(1)
	s.live.Add(1)
	if s.block != nil {
		<-s.block
	}
	return newPipeClient(s.t, &s.closed), nil
}

func managerWith(t *testing.T, s *starter, names ...string) *Manager {
	t.Helper()
	m := NewManagerAt(filepath.Join(t.TempDir(), "mcp.json"))
	for _, n := range names {
		if err := m.Add(n, ServerConfig{Command: "unused"}); err != nil {
			t.Fatal(err)
		}
	}
	s.install(t)
	return m
}

// waitLaunches blocks until at least n launches have begun, so a test can be
// sure it is interleaving with a start in flight rather than racing it.
func (s *starter) waitLaunches(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.launches.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d launches began", s.launches.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// The regression: many callers, one process.
func TestConcurrentEnsureStartsOneProcess(t *testing.T) {
	s := &starter{}
	m := managerWith(t, s, "srv")

	const n = 8
	var wg sync.WaitGroup
	got := make([]*Client, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // maximise the overlap
			got[i], errs[i] = m.ensure(context.Background(), "srv")
		}(i)
	}
	close(start)
	wg.Wait()

	if launches := s.launches.Load(); launches != 1 {
		t.Fatalf("%d processes launched for one server; %d are orphaned", launches, launches-1)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i, c := range got {
		if c == nil || c != got[0] {
			t.Fatalf("caller %d got a different client", i)
		}
	}
	// The one process that was started is still the one the manager holds.
	m.mu.Lock()
	held := m.clients["srv"]
	m.mu.Unlock()
	if held != got[0] {
		t.Fatal("the manager is not holding the client it handed out")
	}
}

// A Remove that lands while a server is starting must not leave the process
// behind: the caller believes that server is gone.
func TestRemoveDuringStartClosesTheProcess(t *testing.T) {
	s := &starter{block: make(chan struct{})}
	m := managerWith(t, s, "srv")

	done := make(chan error, 1)
	go func() {
		_, err := m.ensure(context.Background(), "srv")
		done <- err
	}()

	s.waitLaunches(t, 1)
	if err := m.Remove("srv"); err != nil {
		t.Fatal(err)
	}
	close(s.block)

	if err := <-done; err == nil {
		t.Fatal("starting a server that was just removed should fail")
	}
	if n := s.closed.Load(); n != 1 {
		t.Fatalf("the orphaned process was not closed (%d closes)", n)
	}
	m.mu.Lock()
	_, registered := m.clients["srv"]
	m.mu.Unlock()
	if registered {
		t.Fatal("a removed server was left registered")
	}
}

// The same for Close: a server launched a moment before shutdown must not
// outlive the manager.
func TestCloseDuringStartClosesTheProcess(t *testing.T) {
	s := &starter{block: make(chan struct{})}
	m := managerWith(t, s, "srv")

	done := make(chan error, 1)
	go func() {
		_, err := m.ensure(context.Background(), "srv")
		done <- err
	}()

	s.waitLaunches(t, 1)
	m.Close()
	close(s.block)

	if err := <-done; err == nil {
		t.Fatal("starting a server after shutdown should fail")
	}
	if n := s.closed.Load(); n != 1 {
		t.Fatalf("the orphaned process was not closed (%d closes)", n)
	}
	m.mu.Lock()
	n := len(m.clients)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d clients registered after Close", n)
	}
}

// Once shut down, the manager says so rather than quietly starting things up.
func TestClosedManagerRefusesToStart(t *testing.T) {
	s := &starter{}
	m := managerWith(t, s, "srv")
	m.Close()
	if _, err := m.ensure(context.Background(), "srv"); err == nil {
		t.Fatal("a shut-down manager started a server")
	}
	if s.launches.Load() != 0 {
		t.Fatalf("%d processes launched after shutdown", s.launches.Load())
	}
}

// A failed start must not be cached, or the server could never be retried.
func TestFailedStartIsNotCached(t *testing.T) {
	m := NewManagerAt(filepath.Join(t.TempDir(), "mcp.json"))
	if err := m.Add("bad", ServerConfig{Command: "definitely-not-a-real-binary-xyz"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.ensure(context.Background(), "bad"); err == nil {
			t.Fatalf("attempt %d: expected a start failure", i+1)
		}
	}
	m.mu.Lock()
	n := len(m.clients)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("a failed start left %d clients registered", n)
	}
}

// The per-name lock must not serialise unrelated servers: each start is a
// process launch plus a handshake, so three of them should overlap.
func TestDifferentNamesStartInParallel(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	prev := startClient
	startClient = func(command string, args []string, env map[string]string) (*Client, error) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		inFlight.Add(-1)
		var nothing atomic.Int32
		return newPipeClient(t, &nothing), nil
	}
	defer func() { startClient = prev }()

	m := NewManagerAt(filepath.Join(t.TempDir(), "mcp.json"))
	for _, n := range []string{"a", "b", "c"} {
		if err := m.Add(n, ServerConfig{Command: "unused"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, n := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if _, err := m.ensure(context.Background(), n); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}(n)
	}
	wg.Wait()

	if maxInFlight.Load() < 2 {
		t.Fatalf("starts were serialised across different servers (max %d concurrent)", maxInFlight.Load())
	}
}
