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
	born   time.Time
	// dead is set once the server is killed or its pipe breaks. The scanner
	// is not safe to reuse after an interrupted read, and a server that has
	// stopped answering will not start again, so every later call fails fast
	// instead of blocking.
	dead bool
	// kill is idempotent: it guards the kill/wait in the timeout path.
	killOnce sync.Once
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
	c := &Client{cmd: cmd, born: time.Now()}
	c.stdin = *json.NewEncoder(stdin)
	c.scan = bufio.NewScanner(stdout)
	c.scan.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	return c, nil
}

// callTimeout bounds a request that carries no deadline of its own.
const callTimeout = 60 * time.Second

// killServer stops the process, which is the only portable way to unblock a
// read on its stdout pipe.
func (c *Client) killServer() {
	c.dead = true
	c.killOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
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
	c.dead = true
	if err := c.scan.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mcp %s: server closed stream", method)
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return nil, fmt.Errorf("mcp %s: server is not running (restart it with /mcps tools)", method)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	if err := c.stdin.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.dead = true
		return nil, fmt.Errorf("mcp %s: %w", method, err)
	}

	type outcome struct {
		raw json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		raw, err := c.readResponse(id, method)
		done <- outcome{raw, err}
	}()

	select {
	case r := <-done:
		return r.raw, r.err
	case <-ctx.Done():
		// Killing the server closes the pipe, which releases the reader. Wait
		// (briefly) for it so the scanner is never read by two goroutines.
		c.killServer()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
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

func (c *Client) Close() {
	c.killServer()
}
