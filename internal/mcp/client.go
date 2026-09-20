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

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline := time.Now().Add(60 * time.Second)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	_ = deadline
	if err := c.stdin.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	for c.scan.Scan() {
		var resp rpcResponse
		line := c.scan.Bytes()
		if err := json.Unmarshal(line, &resp); err != nil {
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
	if err := c.scan.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mcp %s: server closed stream", method)
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
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}
