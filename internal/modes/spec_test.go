package modes

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// The whole point of the spec table: a mode is registered once. These tests
// are what stop the rest of the tree from hardcoding a second copy of the
// list and drifting.

func TestSpecsAreInternallyConsistent(t *testing.T) {
	seenSlash := map[string]Mode{}
	seenMode := map[Mode]bool{}
	for _, s := range specs {
		if seenMode[s.mode] {
			t.Fatalf("mode %s is registered twice", s.mode)
		}
		seenMode[s.mode] = true
		if !strings.HasPrefix(s.slash, "/") {
			t.Fatalf("mode %s has a bad slash command %q", s.mode, s.slash)
		}
		if other, dup := seenSlash[s.slash]; dup {
			t.Fatalf("slash command %s is used by both %s and %s", s.slash, other, s.mode)
		}
		seenSlash[s.slash] = s.mode
		if s.summary == "" {
			t.Fatalf("mode %s has no summary (it shows in the palette)", s.mode)
		}
		if s.prompt == nil {
			t.Fatalf("mode %s has no prompt", s.mode)
		}
		// A mode with tools must have a round budget, and vice versa.
		if len(s.tools) > 0 && s.rounds <= 0 {
			t.Fatalf("mode %s has tools but no round budget", s.mode)
		}
		if len(s.tools) == 0 && s.rounds != 0 {
			t.Fatalf("mode %s has no tools but a %d-round budget", s.mode, s.rounds)
		}
		// Every named tool must exist, so a typo cannot silently disable it.
		reg := tools.DefaultRegistry(t.TempDir())
		for _, n := range s.tools {
			if _, ok := reg.Get(n); !ok {
				t.Fatalf("mode %s allow-lists %q, which is not a registered tool", s.mode, n)
			}
		}
	}
}

func TestParseAndNamesAgree(t *testing.T) {
	names := Names()
	if len(names) != len(specs) {
		t.Fatalf("Names() has %d entries, want %d", len(names), len(specs))
	}
	for i, n := range names {
		m, err := Parse(n)
		if err != nil {
			t.Fatalf("Parse(%q): %v", n, err)
		}
		if string(m) != n {
			t.Fatalf("Names()[%d] = %q but Parse gave %q", i, n, m)
		}
		if !Known(m) {
			t.Fatalf("%q is listed but not Known", n)
		}
	}
	// The error message is built from Names(), so it lists every mode.
	_, err := Parse("nope")
	if err == nil {
		t.Fatal("an unknown mode should error")
	}
	for _, n := range names {
		if !strings.Contains(err.Error(), n) {
			t.Fatalf("the error does not mention %q: %v", n, err)
		}
	}
	// Case and whitespace are tolerated.
	for _, in := range []string{"GOAL", "  build  ", "Plan"} {
		if _, err := Parse(in); err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
	}
}

func TestAccessorsMatchTheTable(t *testing.T) {
	for _, s := range specs {
		m := s.mode
		if Rounds(m) != s.rounds {
			t.Fatalf("%s rounds = %d, want %d", m, Rounds(m), s.rounds)
		}
		if UsesRepoContext(m) != s.repoContext {
			t.Fatalf("%s repoContext = %v", m, UsesRepoContext(m))
		}
		if IsReadOnly(m) != s.readOnly {
			t.Fatalf("%s readOnly = %v", m, IsReadOnly(m))
		}
		if Slash(m) != s.slash {
			t.Fatalf("%s slash = %q, want %q", m, Slash(m), s.slash)
		}
		if Summary(m) != s.summary {
			t.Fatalf("%s summary = %q", m, Summary(m))
		}
		got := AllowedTools(m)
		if len(got) != len(s.tools) {
			t.Fatalf("%s allow-list has %d entries, want %d", m, len(got), len(s.tools))
		}
		// Extras are appended, and the base list is never mutated.
		withExtra := AllowedTools(m, "mcp__x__y")
		if len(withExtra) != len(s.tools)+1 {
			t.Fatalf("%s extras were not appended", m)
		}
		if len(AllowedTools(m)) != len(s.tools) {
			t.Fatalf("%s allow-list was mutated by an extra", m)
		}
	}
}

func TestUnknownModeFailsSafe(t *testing.T) {
	if Known("nope") {
		t.Fatal("an unknown mode must not be Known")
	}
	// An unknown mode behaves as chat: no tools, so a bad value cannot write.
	if got := AllowedTools("nope"); len(got) != 0 {
		t.Fatalf("an unknown mode has tools: %v", got)
	}
	if SlashForCommand("/nope") != "" {
		t.Fatal("a non-mode command must not resolve to a mode")
	}
}

func TestSlashForCommand(t *testing.T) {
	for _, s := range specs {
		if got := SlashForCommand(s.slash); got != s.mode {
			t.Fatalf("SlashForCommand(%q) = %q, want %q", s.slash, got, s.mode)
		}
	}
}

// The OpenAPI enum is a second copy of the mode list that lives in another
// file. This is the guard that keeps it honest.
func TestOpenAPIEnumMatchesRegistry(t *testing.T) {
	path := filepath.Join("..", "..", "api", "openapi.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	re := regexp.MustCompile(`enum:\s*\[([^\]]+)\]`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Skip("no enum found in the spec")
	}
	var spec []string
	for _, part := range strings.Split(m[1], ",") {
		spec = append(spec, strings.TrimSpace(part))
	}
	want := Names()
	if len(spec) != len(want) {
		t.Fatalf("the spec lists %v, the registry has %v", spec, want)
	}
	for i := range want {
		if spec[i] != want[i] {
			t.Fatalf("the spec lists %v, the registry has %v", spec, want)
		}
	}
}

// The Tab cycle is shown to the user in /help, so every mode must appear.
func TestTabCycleMentionsEveryMode(t *testing.T) {
	cycle := strings.ToLower(TabCycle())
	for _, s := range specs {
		if !strings.Contains(cycle, string(s.mode)) {
			t.Fatalf("TabCycle() = %q, missing %s", cycle, s.mode)
		}
	}
	// Human-readable, in registry order.
	if !strings.Contains(TabCycle(), "Plan") {
		t.Fatalf("TabCycle() = %q should be presentable", TabCycle())
	}
	if !strings.HasPrefix(TabCycle(), "Plan") {
		t.Fatalf("TabCycle() = %q should start with the first registered mode", TabCycle())
	}
}
