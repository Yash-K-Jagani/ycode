package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/agent/fakeprovider"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// failTool returns a distinctive error plus the output it managed to produce —
// the shape of run/testgen/notebook/scaffold/patch, which all return real
// stdout or a traceback alongside the failure.
type failTool struct {
	out string
}

func (failTool) Name() string        { return "boom" }
func (failTool) Description() string { return "always fails with output" }
func (failTool) Schema() string      { return `{"type":"object"}` }
func (f failTool) Run(context.Context, json.RawMessage) (string, error) {
	return f.out, fmt.Errorf("exit status 1")
}

func regWith(t tools.Tool) (*tools.Registry, []string) {
	r := tools.NewRegistry()
	r.Add(t)
	return r, []string{"boom"}
}

// The model must see the tool's output, not just the error string. Overwriting
// it meant a Python KeyError reached the model as "ERROR: exit status 1" — with
// no traceback — so it had nothing to correct itself from.
func TestToolFailureKeepsItsOutput(t *testing.T) {
	trace := "Traceback (most recent call last):\n  File \"x.py\", line 3\nKeyError: 'config'"
	reg, allowed := regWith(failTool{out: trace})
	p := fakeprovider.New(`<tool:boom>{}</tool:boom>`, "no more calls")

	_, err := Run(context.Background(), p, "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "run it"}},
		reg, allowed, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := p.Seen()
	if len(seen) < 2 {
		t.Fatalf("expected a follow-up request carrying the tool result, got %d", len(seen))
	}
	// The result block is fed back to the model as a system message.
	var result string
	for _, msg := range seen[len(seen)-1] {
		if strings.Contains(msg.Content, "<tool_result:") {
			result = msg.Content
		}
	}
	if result == "" {
		t.Fatal("no tool_result was sent back to the model")
	}
	if !strings.Contains(result, "exit status 1") {
		t.Fatalf("the error should be present:\n%s", result)
	}
	if !strings.Contains(result, "KeyError: 'config'") {
		t.Fatalf("the tool's output was discarded — the model cannot self-correct:\n%s", result)
	}
}

func TestToolErrorRendering(t *testing.T) {
	// No output: just the error.
	if got := toolError(fmt.Errorf("boom"), ""); got != "ERROR: boom" {
		t.Fatalf("toolError = %q", got)
	}
	if got := toolError(fmt.Errorf("boom"), "   \n "); got != "ERROR: boom" {
		t.Fatalf("blank output should be dropped, got %q", got)
	}
	// Output is kept and labelled.
	got := toolError(fmt.Errorf("boom"), "line1\nline2")
	if !strings.Contains(got, "ERROR: boom") || !strings.Contains(got, "line1") {
		t.Fatalf("toolError = %q", got)
	}
	// A runaway command must not flood the context.
	huge := strings.Repeat("x", 5000)
	got = toolError(fmt.Errorf("boom"), huge)
	if len(got) > 4200 {
		t.Fatalf("toolError did not cap output: %d bytes", len(got))
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("truncation should be visible, got %q", got[len(got)-40:])
	}
}

// A tool that fails must not be cached as if it had succeeded: the repeat
// guard keys on name+args, and a cached success would hide the failure.
func TestFailedCallIsNotCachedAsSuccess(t *testing.T) {
	reg, allowed := regWith(failTool{out: "partial"})
	p := fakeprovider.New(`<tool:boom>{}</tool:boom>`, "second")
	_, err := Run(context.Background(), p, "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "go"}},
		reg, allowed, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.Requests() < 2 {
		t.Fatalf("expected a second round, got %d requests", p.Requests())
	}
}

// The Exec path (bare shell commands converted to tool calls) shares the
// rendering, so assert it there too.
func TestExecKeepsToolOutput(t *testing.T) {
	reg, allowed := regWith(failTool{out: "stderr detail here"})
	results := Exec(context.Background(), reg, allowed, nil,
		[]Call{{Name: "boom", Args: json.RawMessage(`{}`)}},
		func(name, args, result string, err error) {})
	if len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	if !strings.Contains(results[0], "stderr detail here") {
		t.Fatalf("Exec dropped the tool output: %q", results[0])
	}
	if !strings.Contains(results[0], "exit status 1") {
		t.Fatalf("Exec dropped the error: %q", results[0])
	}
}

// Sanity: a real tool's failure output really does arrive, end to end.
func TestRealToolFailureOutputReachesTheModel(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no POSIX shell")
	}
	dir := t.TempDir()
	r := tools.NewRegistry()
	r.Add(tools.NewBashTool(dir))
	p := fakeprovider.New(`<tool:bash>{"cmd":"echo MARKER-OUTPUT; exit 3"}</tool:bash>`, "done")
	_, err := Run(context.Background(), p, "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "go"}},
		r, []string{"bash"}, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, msgs := range p.Seen() {
		for _, msg := range msgs {
			if strings.Contains(msg.Content, "MARKER-OUTPUT") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the command's output never reached the model")
	}
}
