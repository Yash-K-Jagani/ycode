package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerConfigRoundTrip(t *testing.T) {
	m := NewManagerAt(filepath.Join(t.TempDir(), "mcp.json"))
	if err := m.Add("s1", ServerConfig{Command: "nope-missing-bin"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("s1", ServerConfig{Command: "x"}); err == nil {
		t.Fatal("expected duplicate error")
	}
	if names := m.Names(); len(names) != 1 || names[0] != "s1" {
		t.Fatalf("bad names: %v", names)
	}
	if err := m.Remove("nope"); err == nil {
		t.Fatal("expected unknown-server error")
	}
	if err := m.Remove("s1"); err != nil {
		t.Fatal(err)
	}
	// Tools() against a missing binary must error, not hang
	if _, err := m.Tools(context.Background()); err == nil {
		t.Log("no servers configured — nil error is fine")
	}
	_ = m.Add("bad", ServerConfig{Command: "definitely-not-a-real-binary-xyz"})
	if _, err := m.Tools(context.Background()); err == nil {
		t.Fatal("expected start failure for bad binary")
	}
}

// fakeServer speaks enough MCP to satisfy ListTools + CallTool + initialize.
func fakeServer(t *testing.T, r *os.File, w *os.File) {
	t.Helper()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		var req map[string]any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		id, hasID := req["id"]
		if !hasID {
			continue // notification
		}
		method, _ := req["method"].(string)
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "upper", "description": "uppercase",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "HELLO"}}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
}

func TestClientProtocol(t *testing.T) {
	c2sR, c2sW, _ := os.Pipe() // client writes, server reads
	s2cR, s2cW, _ := os.Pipe() // server writes, client reads
	defer func() { _ = c2sR.Close() }()
	defer func() { _ = c2sW.Close() }()
	defer func() { _ = s2cR.Close() }()
	defer func() { _ = s2cW.Close() }()
	go fakeServer(t, c2sR, s2cW)

	c := &Client{stdin: *json.NewEncoder(c2sW)}
	c.scan = bufio.NewScanner(s2cR)
	c.scan.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defs, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "upper" {
		t.Fatalf("bad tools: %+v", defs)
	}
	out, err := c.CallTool(ctx, "upper", json.RawMessage(`{"t":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HELLO") {
		t.Fatalf("bad call result: %q", out)
	}
}
