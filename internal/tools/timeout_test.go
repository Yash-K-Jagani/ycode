package tools

import (
	"testing"
	"time"
)

// The agent loop used to cap every tool call at 90s, which silently overrode the
// longer budgets the tools declare: a two-minute `go build` or `npm install`
// was killed and reported as "context deadline exceeded".
func TestTimeoutForKnowsLongTools(t *testing.T) {
	long := map[string]time.Duration{
		"testgen":        5 * time.Minute,
		"notebook":       5 * time.Minute,
		"github":         5 * time.Minute,
		"scaffold":       10 * time.Minute,
		"mcp__srv__tool": 5 * time.Minute,
		"plugin__mine":   5 * time.Minute,
	}
	for name, want := range long {
		if got := TimeoutFor(name); got < 90*time.Second {
			t.Fatalf("TimeoutFor(%q) = %s; the old 90s cap would kill it", name, got)
		}
		if got := TimeoutFor(name); got != want {
			t.Fatalf("TimeoutFor(%q) = %s, want %s", name, got, want)
		}
	}
	short := map[string]time.Duration{
		"bash":     60 * time.Second,
		"db":       30 * time.Second,
		"api":      30 * time.Second,
		"browser":  30 * time.Second,
		"git":      60 * time.Second,
		"read":     defaultToolTimeout,
		"newthing": defaultToolTimeout,
	}
	for name, want := range short {
		if got := TimeoutFor(name); got != want {
			t.Fatalf("TimeoutFor(%q) = %s, want %s", name, got, want)
		}
	}
	// Nothing may be unbounded.
	for _, n := range []string{"", "read", "bash", "testgen", "mcp__x__y"} {
		if TimeoutFor(n) <= 0 {
			t.Fatalf("TimeoutFor(%q) is not positive", n)
		}
	}
}
