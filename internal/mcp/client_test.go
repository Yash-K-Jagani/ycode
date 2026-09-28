package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperSilentServer is not a test: re-executed as a child process it acts
// as an MCP server that starts, reads nothing and answers nothing. There is no
// cross-platform "silent process" to point exec at, and this is the only way
// to reproduce the real hang.
func TestHelperSilentServer(t *testing.T) {
	if os.Getenv("YCODE_MCP_SILENT") != "1" {
		t.Skip("helper process")
	}
	select {} // accept the request and never reply
}

func startSilentServer(t *testing.T) *Client {
	t.Helper()
	c, err := Start(os.Args[0], []string{"-test.run=TestHelperSilentServer"}, map[string]string{
		"YCODE_MCP_SILENT": "1",
	})
	if err != nil {
		t.Fatalf("start silent server: %v", err)
	}
	return c
}

// A server that never answers used to hang ycode forever: the call held c.mu,
// so every later call on that server blocked behind it, and Esc could not help
// because cancelling the turn context had no effect — the code computed a
// deadline and then discarded it.
func TestCallTimesOutOnSilentServer(t *testing.T) {
	c := startSilentServer(t)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.ListTools(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error from a silent server")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("the call took %s; the deadline was not honoured", elapsed)
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("error should say it timed out, got %v", err)
	}
}

// After a timeout the client is poisoned: a server that stopped answering will
// not start again, and the scanner must not be read by a second goroutine, so
// later calls fail immediately instead of blocking.
func TestClientIsPoisonedAfterTimeout(t *testing.T) {
	c := startSilentServer(t)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := c.ListTools(ctx); err == nil {
		t.Fatal("expected a timeout")
	}

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

// A missing command must still be an error, not a hang.
func TestStartFailsForMissingCommand(t *testing.T) {
	if _, err := Start("definitely-not-a-real-command-xyz", nil, nil); err == nil {
		t.Fatal("Start should fail for a missing command")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	c := startSilentServer(t)
	c.Close()
	c.Close() // must not panic on an already-reaped process
}
