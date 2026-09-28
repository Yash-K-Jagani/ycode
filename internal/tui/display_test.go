package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// These are the small display and lookup functions the TUI leans on. None of
// them is hard, but each one produces output a user reads and a wrong answer
// is indistinguishable from a bug in the thing being described.

func TestCtxBar(t *testing.T) {
	// A zero budget means "we do not know yet", which must not render as
	// 0% - that reads as a full context window.
	if got := ctxBar(100, 0); strings.Contains(got, "0%") {
		t.Fatalf("an unknown budget rendered as %q", got)
	}
	half := ctxBar(500, 1000)
	if !strings.Contains(half, "50%") {
		t.Fatalf("50%% rendered as %q", half)
	}
	// Clamped at both ends: over budget must not show 340%, and a negative
	// reading must not show -5%.
	if got := ctxBar(3400, 1000); !strings.Contains(got, "100%") {
		t.Fatalf("over budget rendered as %q", got)
	}
	if got := ctxBar(-5, 1000); !strings.Contains(got, "0%") {
		t.Fatalf("a negative reading rendered as %q", got)
	}
	// Tokens are abbreviated so the bar fits the sidebar.
	if got := ctxBar(3700, 6000); !strings.Contains(got, "3.7k/6.0k") {
		t.Fatalf("abbreviated counts missing from %q", got)
	}
}

func TestShortTokens(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1.0k", 3700: "3.7k", 12345: "12.3k"} {
		if got := shortTokens(in); got != want {
			t.Fatalf("shortTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncSide(t *testing.T) {
	short := "short"
	if got := truncSide(short); got != short {
		t.Fatalf("a short name was altered: %q", got)
	}
	long := strings.Repeat("x", sideWidth*2)
	got := truncSide(long)
	if len([]rune(got)) > sideWidth {
		t.Fatalf("truncSide left %d runes, wider than the %d sidebar", len([]rune(got)), sideWidth)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a truncated name should end in an ellipsis: %q", got)
	}
	// Every rendered row must fit, or the panel border breaks.
	for _, s := range []string{"", "a", strings.Repeat("y", sideWidth), long} {
		if w := len([]rune(truncSide(s))); w > sideWidth {
			t.Fatalf("truncSide(%d runes) = %d runes", len([]rune(s)), w)
		}
	}
}

// restOrEmpty joins the arguments after position i, for commands that take
// trailing free text.
func TestRestOrEmpty(t *testing.T) {
	f := []string{"git", "commit", "-m", "fix the thing"}
	if got := restOrEmpty(f, 2); got != "-m fix the thing" {
		t.Fatalf("restOrEmpty = %q", got)
	}
	if got := restOrEmpty(f, 4); got != "" {
		t.Fatalf("an index at the end gave %q", got)
	}
	if got := restOrEmpty(f, 99); got != "" {
		t.Fatalf("an out-of-range index gave %q", got)
	}
	if got := restOrEmpty(nil, 0); got != "" {
		t.Fatalf("an empty list gave %q", got)
	}
}

// truncateForRag caps what goes into a vector index. Silently indexing a
// truncated prompt would make retrieval return matches for a document the user
// never stored, so the elision is marked.
func TestTruncateForRag(t *testing.T) {
	short := "a short prompt"
	if got := truncateForRag(short); got != short {
		t.Fatalf("a short prompt was altered: %q", got)
	}
	long := strings.Repeat("z", 2000)
	got := truncateForRag(long)
	if len(got) > 1300 {
		t.Fatalf("truncateForRag returned %d chars", len(got))
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("a truncated prompt is not marked: %q", got[:40])
	}
}

// hasKey decides which providers /connect offers without stopping to ask for a
// credential. It is derived from the registry, so a new provider is handled.
func TestHasKeyFollowsTheRegistry(t *testing.T) {
	m := &Model{cfg: config.Defaults()}
	for _, s := range providers.All() {
		got := hasKey(m, s.ID)
		want := !s.NeedsKey
		if got != want {
			t.Fatalf("%s: hasKey = %v with no key set, want %v", s.ID, got, want)
		}
		// Supplying the key must flip exactly that provider.
		m.cfg.SetKeyFor(s.ID, "k")
		if hasKey(m, s.ID) != true {
			t.Fatalf("%s: hasKey = false after the key was set", s.ID)
		}
		m.cfg.SetKeyFor(s.ID, "")
	}
	// An unknown provider cannot be blocked on a key it does not have.
	if !hasKey(m, "nope") {
		t.Fatal("an unknown provider should not be reported as keyless")
	}
}

// A zero or negative TTL would silently disable the sidebar cache and put the
// disk I/O back on the render path; a very long one would make the panel lie.
func TestSidebarTTLIsSane(t *testing.T) {
	if sidebarTTL <= 0 || sidebarTTL > 30*time.Second {
		t.Fatalf("sidebarTTL = %s, which defeats the cache", sidebarTTL)
	}
}
