package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client speaks newline-delimited JSON-RPC 2.0 over a server process's stdio.
type Client struct {
	cmd    *exec.Cmd
	stdin  json.Encoder
	scan   *bufio.Scanner
	mu     sync.Mutex
	nextID int64
	// dead is set once the server is killed or its pipe breaks. The scanner
	// is not safe to reuse after an interrupted read, and a server that has
	// stopped answering will not start again, so every later call fails fast
	// instead of blocking.
	//
	// It is atomic rather than guarded by mu because the writers cannot all
	// hold the lock: readResponse runs on its own goroutine, and Close is
	// called without the lock. A plain bool raced with call's check, which
	// the race detector caught: a call after a timeout read it while the
	// previous reader goroutine was still writing it.
	dead atomic.Bool
	// kill is idempotent: it guards the kill in the timeout path.
	killOnce sync.Once
	// waitOnce guards the reap, which must happen after every read has
	// finished. Kept apart from killOnce because the two are not the same
	// moment: the process is killed while the reader is still draining, and
	// only reaped once it is not.
	waitOnce sync.Once
	// readers counts goroutines inside readResponse. Close only reaps when
	// this is zero, because reaping under an active reader is the documented
	// incorrect ordering.
	readers atomic.Int32
	// closeHook is a test seam for observing that Close was reached. It is nil
	// in production and costs one branch on a path that already does a kill.
	closeHook func()
}

func Start(command string, args []string, env map[string]string) (*Client, error) {
	cmd := exec.Command(command, args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd}
	c.stdin = *json.NewEncoder(stdin)
	c.scan = bufio.NewScanner(stdout)
	c.scan.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	return c, nil
}

// callTimeout bounds a request that carries no deadline of its own.
const callTimeout = 60 * time.Second

// killServer stops the process, which is the only portable way to unblock a
// read on its stdout pipe.
//
// It deliberately does not Wait. os/exec documents that it is "incorrect to
// call Wait before all reads from the pipe have completed", because Wait
// closes the pipe once the command exits - and the reader goroutine spawned by
// call is normally still in Scan at exactly this moment. Waiting here can
// block indefinitely, and because Manager.Close holds the manager lock across
// Close, that turns into a deadlock of everything touching the manager rather
// than one stuck request. Reaping is reap's job, and it only runs once the
// reads are finished.
func (c *Client) killServer() {
	c.dead.Store(true)
	c.killOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

// reap collects the exited process. It must not be called until every read
// from the stdout pipe has finished.
func (c *Client) reap() {
	c.waitOnce.Do(func() {
		if c.cmd != nil {
			_ = c.cmd.Wait()
		}
	})
}

// readResponse reads lines until the response to id arrives.
//
// It runs on its own goroutine so a silent server cannot outlive the
// deadline: Scan blocks on the pipe, and nothing in the stdlib lets you bound
// that, so the timeout path kills the server instead.
func (c *Client) readResponse(id int64, method string) (json.RawMessage, error) {
	for c.scan.Scan() {
		var resp rpcResponse
		if err := json.Unmarshal(c.scan.Bytes(), &resp); err != nil {
			continue
		}
		if resp.ID == nil || *resp.ID != id {
			continue // notification or unrelated
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
	c.dead.Store(true)
	if err := c.scan.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mcp %s: server closed stream", method)
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead.Load() {
		return nil, fmt.Errorf("mcp %s: server is not running (restart it with /mcps tools)", method)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	if err := c.stdin.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.dead.Store(true)
		return nil, fmt.Errorf("mcp %s: %w", method, err)
	}

	type outcome struct {
		raw json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		c.readers.Add(1)
		raw, err := c.readResponse(id, method)
		c.readers.Add(-1)
		done <- outcome{raw, err}
	}()

	select {
	case r := <-done:
		return r.raw, r.err
	case <-ctx.Done():
		// Killing the server closes the pipe, which releases the reader. Wait
		// for it before reaping, so Wait never runs while a read is in flight.
		c.killServer()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		c.reap()
		return nil, fmt.Errorf("mcp %s: %w", method, ctx.Err())
	}
}

func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ycode", "version": "0.3.0"},
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdin.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var v struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var parsed any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &parsed)
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": parsed})
	if err != nil {
		return "", err
	}
	var v struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw), nil
	}
	out := ""
	for _, c := range v.Content {
		out += c.Text
	}
	if v.IsError {
		return out, fmt.Errorf("mcp tool error: %s", out)
	}
	if out == "" {
		return string(raw), nil
	}
	return out, nil
}

// Close stops the server. It reaps the process only when no read is in flight;
// if one is, that reader's owner reaps when it finishes, which is the only
// ordering os/exec allows.
func (c *Client) Close() {
	c.killServer()
	if c.readers.Load() == 0 {
		c.reap()
	}
	if c.closeHook != nil {
		c.closeHook()
	}
}
