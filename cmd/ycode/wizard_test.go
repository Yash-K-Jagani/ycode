package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// The setup wizard is the first thing a new user sees, and promptInt's retry
// loop is the part most likely to trap someone: it re-asks until the input
// parses, and a mistake there is an infinite loop with a new user watching.

// withStdin replaces os.Stdin with a pipe carrying the given input, for the
// duration of the test. os.Stdin is a var, so this works; the tests that use it
// do not run in parallel.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(w, input)
		_ = w.Close()
	}()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})
}

func TestPromptLine(t *testing.T) {
	withStdin(t, "hello world\n")
	got, ok := promptLine("name")
	if !ok {
		t.Fatal("promptLine reported no input")
	}
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptLineTrims(t *testing.T) {
	withStdin(t, "   spaced out   \n")
	got, ok := promptLine("name")
	if !ok || got != "spaced out" {
		t.Fatalf("got %q, ok=%v", got, ok)
	}
}

// EOF must be reported rather than returning an empty answer, or the wizard
// would proceed as though the user had confirmed something.
func TestPromptLineOnEOF(t *testing.T) {
	withStdin(t, "")
	if got, ok := promptLine("name"); ok {
		t.Fatalf("EOF reported as input %q", got)
	}
}

func TestPromptInt(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		def      int
		min, max int
		want     int
		wantOK   bool
	}{
		{"plain", "3\n", 1, 1, 5, 3, true},
		{"default on empty", "\n", 2, 1, 5, 2, true},
		{"at the top", "1\n", 1, 1, 5, 1, true},
		{"at the bottom", "5\n", 1, 1, 5, 5, true},
		// Out of range and non-numeric are both rejected, and the next line
		// is used.
		{"below range then valid", "0\n4\n", 1, 1, 5, 4, true},
		{"above range then valid", "9\n2\n", 1, 1, 5, 2, true},
		{"not a number then valid", "abc\n3\n", 1, 1, 5, 3, true},
		// Exhausted input gives up rather than looping forever.
		{"EOF while retrying", "nope\n", 1, 1, 5, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withStdin(t, tc.in)
			got, ok := promptInt("pick", tc.def, tc.min, tc.max)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %d)", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// promptSecret has to work when the terminal cannot be switched to raw mode,
// which is exactly the case in a pipe and in CI. Falling back to a plain read
// is correct; failing is not.
func TestPromptSecretFallsBackWithoutATerminal(t *testing.T) {
	withStdin(t, "sk-secret-value\n")
	got, ok := promptSecret("api key")
	if !ok {
		t.Fatal("promptSecret reported no input")
	}
	if got != "sk-secret-value" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptSecretOnEOF(t *testing.T) {
	withStdin(t, "")
	if got, ok := promptSecret("api key"); ok {
		t.Fatalf("EOF reported as a key: %q", got)
	}
}

// isFirstRun only asks whether config.yaml exists. An empty one still counts as
// "been here", and that is right: onboard decides whether setup is needed from
// SetupNeeded, which reports a missing model. The two questions are separate.
func TestIsFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if !isFirstRun() {
		t.Fatal("a home with no config should be a first run")
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(config.Dir(), "config.yaml")
	if err := os.WriteFile(cfgFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if isFirstRun() {
		t.Fatal("an existing config file should not be a first run")
	}
	// And an empty one still leaves setup to run, by the other route.
	if config.SetupNeeded(config.Defaults()).Ready {
		t.Fatal("an empty config reports as ready")
	}
}

// chooseModel has to give up cleanly when the daemon is unreachable, naming
// the address, rather than hanging a new user on a wizard.
func TestChooseModelReportsAnUnreachableDaemon(t *testing.T) {
	cfg := config.Defaults()
	cfg.OllamaHost = "http://127.0.0.1:1" // nothing listens here
	if _, ok := chooseModel("ollama", &cfg); ok {
		t.Fatal("chooseModel claimed success against a dead daemon")
	}
}

// A cloud provider asks for a model id rather than probing, so it must not
// touch the network - and must decline on empty input.
func TestChooseModelAsksForACloudModelID(t *testing.T) {
	cfg := config.Defaults()
	withStdin(t, "\n")
	if _, ok := chooseModel("gemini", &cfg); ok {
		t.Fatal("an empty model id was accepted")
	}
	withStdin(t, "gemini-2.5-pro\n")
	got, ok := chooseModel("gemini", &cfg)
	if !ok || got != "gemini-2.5-pro" {
		t.Fatalf("got %q, ok=%v", got, ok)
	}
}

// buildInfo is what a user pastes into a bug report, so a lost placeholder
// would tell them nothing.
func TestBuildInfoIsComplete(t *testing.T) {
	got := buildInfo()
	for _, want := range []string{"ycode", "commit", runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(got, want) {
			t.Fatalf("buildInfo = %q, missing %q", got, want)
		}
	}
}

// configPairs is what `ycode config list` prints, and it is routinely pasted
// into issues - so a key must never appear in full.
func TestConfigPairsNeverShowAKey(t *testing.T) {
	c := config.Defaults()
	for _, id := range providers.IDs() {
		c.SetKeyFor(id, "secret-"+id+"-value")
	}
	for _, kv := range configPairs(c) {
		key, val := kv[0], kv[1]
		if strings.Contains(val, "secret-") {
			t.Fatalf("%s printed a key: %q", key, val)
		}
		if isSecretConfigKey(key) {
			if val != "set" {
				t.Fatalf("%s should report only whether it is set, got %q", key, val)
			}
		} else if val == "set" {
			t.Fatalf("%s is not a key but reports as one", key)
		}
	}
	// An unset key is distinguishable from a set one without leaking anything.
	c2 := config.Defaults()
	var sawUnset bool
	for _, kv := range configPairs(c2) {
		if isSecretConfigKey(kv[0]) && kv[1] == "unset" {
			sawUnset = true
		}
	}
	if !sawUnset {
		t.Fatal("an unset key was not reported as unset")
	}
}

func TestTruncateShortensForDisplay(t *testing.T) {
	if got := truncate("hello", 100); got != "hello" {
		t.Fatalf("short text altered: %q", got)
	}
	if n := len([]rune(truncate(strings.Repeat("x", 50), 10))); n > 11 {
		t.Fatalf("truncate returned %d characters", n)
	}
}
