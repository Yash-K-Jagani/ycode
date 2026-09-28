package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/agent/fakeprovider"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Zero-Data-Leak mode is a guardrail, and the classification of what reaches
// the network now lives in one place so no tool can forget to declare itself.
func TestReachesNetworkClassification(t *testing.T) {
	for _, n := range []string{"api", "browser", "github", "git", "db", "notebook", "scaffold", "vscode"} {
		if !tools.ReachesNetwork(n) {
			t.Fatalf("%s reaches the network but is not classified as such", n)
		}
	}
	// MCP and plugin tools are opaque shell commands.
	for _, n := range []string{"mcp__github__create_issue", "plugin__mytool", "MCP__Srv__X"} {
		if !tools.ReachesNetwork(n) {
			t.Fatalf("%s should be treated as network-capable", n)
		}
	}
	for _, n := range []string{"read", "write", "edit", "grep", "glob", "tree", "todo", "changes", "patch", "run", "bash", "testgen", "security", "a-brand-new-tool"} {
		if tools.ReachesNetwork(n) {
			t.Fatalf("%s does not reach the network but is classified as such", n)
		}
	}
	// An unlisted tool is assumed local, so adding one is safe by default.
	if tools.ReachesNetwork("some_future_network_tool") {
		t.Fatal("unknown tools should default to local")
	}
	if len(tools.NetworkTools()) < 8 {
		t.Fatalf("NetworkTools() = %v", tools.NetworkTools())
	}
}

// The check lives in the loop, so a tool that forgets it is still stopped.
func TestZeroLeakBlocksNetworkToolsInTheLoop(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.BrowserTool{})
	p := fakeprovider.New(`<tool:browser>{"url":"https://example.com"}</tool:browser>`, "done")

	ctx := tools.WithZeroLeak(context.Background())
	_, err := Run(ctx, p, "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "fetch it"}},
		reg, []string{"browser"}, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	blocked := ""
	for _, msgs := range p.Seen() {
		for _, msg := range msgs {
			if strings.Contains(msg.Content, "<tool_result:") {
				blocked = msg.Content
			}
		}
	}
	if blocked == "" {
		t.Fatal("no tool result was sent back")
	}
	if !strings.Contains(blocked, "zero-data-leak") {
		t.Fatalf("the model was not told the tool is blocked in ZDL mode:\n%s", blocked)
	}
}

// Local tools keep working under ZDL — otherwise the mode is useless.
func TestZeroLeakAllowsLocalTools(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Add(&tools.ReadTool{})
	p := fakeprovider.New(`<tool:read>{"path":"go.mod"}</tool:read>`, "done")

	_, err := Run(tools.WithZeroLeak(context.Background()), p, "m",
		[]apitypes.Message{{Role: apitypes.RoleUser, Content: "read it"}},
		reg, []string{"read"}, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, msgs := range p.Seen() {
		for _, msg := range msgs {
			if strings.Contains(msg.Content, "zero-data-leak") {
				t.Fatalf("a local tool was blocked in ZDL mode:\n%s", msg.Content)
			}
		}
	}
}

func TestBlockNetworkReasonIsActionable(t *testing.T) {
	err := tools.BlockNetworkReason("api")
	msg := err.Error()
	for _, want := range []string{"api", "zero_data_leak", "local only"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("reason missing %q: %s", want, msg)
		}
	}
}
